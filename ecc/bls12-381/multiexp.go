// Copyright 2020 ConsenSys Software Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Code generated by consensys/gnark-crypto DO NOT EDIT

package bls12381

import (
	"errors"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bls12-381/fr"
	"github.com/consensys/gnark-crypto/internal/parallel"
	"math"
	"runtime"
)

// selector stores the index, mask and shifts needed to select bits from a scalar
// it is used during the multiExp algorithm or the batch scalar multiplication
type selector struct {
	index uint64 // index in the multi-word scalar to select bits from
	mask  uint64 // mask (c-bit wide)
	shift uint64 // shift needed to get our bits on low positions

	multiWordSelect bool   // set to true if we need to select bits from 2 words (case where c doesn't divide 64)
	maskHigh        uint64 // same than mask, for index+1
	shiftHigh       uint64 // same than shift, for index+1
}

// partitionScalars  compute, for each scalars over c-bit wide windows, nbChunk digits
// if the digit is larger than 2^{c-1}, then, we borrow 2^c from the next window and substract
// 2^{c} to the current digit, making it negative.
// negative digits can be processed in a later step as adding -G into the bucket instead of G
// (computing -G is cheap, and this saves us half of the buckets in the MultiExp or BatchScalarMultiplication)
// scalarsMont indicates wheter the provided scalars are in montgomery form
// returns smallValues, which represent the number of scalars which meets the following condition
// 0 < scalar < 2^c (in other words, scalars where only the c-least significant bits are non zero)
func partitionScalars(scalars []fr.Element, c uint64, scalarsMont bool, nbTasks int) ([]fr.Element, int) {
	toReturn := make([]fr.Element, len(scalars))

	// number of c-bit radixes in a scalar
	nbChunks := fr.Limbs * 64 / c
	if (fr.Limbs*64)%c != 0 {
		nbChunks++
	}

	mask := uint64((1 << c) - 1)      // low c bits are 1
	msbWindow := uint64(1 << (c - 1)) // msb of the c-bit window
	max := int(1 << (c - 1))          // max value we want for our digits
	cDivides64 := (64 % c) == 0       // if c doesn't divide 64, we may need to select over multiple words

	// compute offset and word selector / shift to select the right bits of our windows
	selectors := make([]selector, nbChunks)
	for chunk := uint64(0); chunk < nbChunks; chunk++ {
		jc := uint64(chunk * c)
		d := selector{}
		d.index = jc / 64
		d.shift = jc - (d.index * 64)
		d.mask = mask << d.shift
		d.multiWordSelect = !cDivides64 && d.shift > (64-c) && d.index < (fr.Limbs-1)
		if d.multiWordSelect {
			nbBitsHigh := d.shift - uint64(64-c)
			d.maskHigh = (1 << nbBitsHigh) - 1
			d.shiftHigh = (c - nbBitsHigh)
		}
		selectors[chunk] = d
	}

	// for each chunk, we could track the number of non-zeros points we will need to process
	// this way, if a chunk has more work to do than others, we can spawn off more go routines
	// (at the cost of more buckets allocated)
	// a simplified approach is to track the small values where only the first word is set
	// if this number represent a significant number of points, then we will split first chunk
	// processing in the msm in 2, to ensure all go routines finish at ~same time
	// /!\ nbTasks is enough as parallel.Execute is not going to spawn more than nbTasks go routine
	// if it does, though, this will deadlocK.
	chSmallValues := make(chan int, nbTasks)

	parallel.Execute(len(scalars), func(start, end int) {
		smallValues := 0
		for i := start; i < end; i++ {
			var carry int

			scalar := scalars[i]
			if scalarsMont {
				scalar.FromMont()
			}
			if scalar.FitsOnOneWord() {
				// everything is 0, no need to process this scalar
				if scalar[0] == 0 {
					continue
				}
				// low c-bits are 1 in mask
				if scalar[0]&mask == scalar[0] {
					smallValues++
				}
			}

			// for each chunk in the scalar, compute the current digit, and an eventual carry
			for chunk := uint64(0); chunk < nbChunks; chunk++ {
				s := selectors[chunk]

				// init with carry if any
				digit := carry
				carry = 0

				// digit = value of the c-bit window
				digit += int((scalar[s.index] & s.mask) >> s.shift)

				if s.multiWordSelect {
					// we are selecting bits over 2 words
					digit += int(scalar[s.index+1]&s.maskHigh) << s.shiftHigh
				}

				// if digit is zero, no impact on result
				if digit == 0 {
					continue
				}

				// if the digit is larger than 2^{c-1}, then, we borrow 2^c from the next window and substract
				// 2^{c} to the current digit, making it negative.
				if digit >= max {
					digit -= (1 << c)
					carry = 1
				}

				var bits uint64
				if digit >= 0 {
					bits = uint64(digit)
				} else {
					bits = uint64(-digit-1) | msbWindow
				}

				toReturn[i][s.index] |= (bits << s.shift)
				if s.multiWordSelect {
					toReturn[i][s.index+1] |= (bits >> s.shiftHigh)
				}

			}
		}

		chSmallValues <- smallValues

	}, nbTasks)

	// aggregate small values
	close(chSmallValues)
	smallValues := 0
	for o := range chSmallValues {
		smallValues += o
	}
	return toReturn, smallValues
}

// MultiExp implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G1Affine) MultiExp(points []G1Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G1Affine, error) {
	var _p G1Jac
	if _, err := _p.MultiExp(points, scalars, config); err != nil {
		return nil, err
	}
	p.FromJacobian(&_p)
	return p, nil
}

// MultiExp implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G1Jac) MultiExp(points []G1Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G1Jac, error) {
	// note:
	// each of the msmCX method is the same, except for the c constant it declares
	// duplicating (through template generation) these methods allows to declare the buckets on the stack
	// the choice of c needs to be improved:
	// there is a theoritical value that gives optimal asymptotics
	// but in practice, other factors come into play, including:
	// * if c doesn't divide 64, the word size, then we're bound to select bits over 2 words of our scalars, instead of 1
	// * number of CPUs
	// * cache friendliness (which depends on the host, G1 or G2... )
	//	--> for example, on BN254, a G1 point fits into one cache line of 64bytes, but a G2 point don't.

	// for each msmCX
	// step 1
	// we compute, for each scalars over c-bit wide windows, nbChunk digits
	// if the digit is larger than 2^{c-1}, then, we borrow 2^c from the next window and substract
	// 2^{c} to the current digit, making it negative.
	// negative digits will be processed in the next step as adding -G into the bucket instead of G
	// (computing -G is cheap, and this saves us half of the buckets)
	// step 2
	// buckets are declared on the stack
	// notice that we have 2^{c-1} buckets instead of 2^{c} (see step1)
	// we use jacobian extended formulas here as they are faster than mixed addition
	// msmProcessChunk places points into buckets base on their selector and return the weighted bucket sum in given channel
	// step 3
	// reduce the buckets weigthed sums into our result (msmReduceChunk)

	// ensure len(points) == len(scalars)
	nbPoints := len(points)
	if nbPoints != len(scalars) {
		return nil, errors.New("len(points) != len(scalars)")
	}

	// if nbTasks is not set, use all available CPUs
	if config.NbTasks <= 0 {
		config.NbTasks = runtime.NumCPU()
	} else if config.NbTasks > 1024 {
		return nil, errors.New("invalid config: config.NbTasks > 1024")
	}

	// here, we compute the best C for nbPoints
	// we split recursively until nbChunks(c) >= nbTasks,
	bestC := func(nbPoints int) uint64 {
		// implemented msmC methods (the c we use must be in this slice)
		implementedCs := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}
		var C uint64
		// approximate cost (in group operations)
		// cost = bits/c * (nbPoints + 2^{c})
		// this needs to be verified empirically.
		// for example, on a MBP 2016, for G2 MultiExp > 8M points, hand picking c gives better results
		min := math.MaxFloat64
		for _, c := range implementedCs {
			cc := fr.Limbs * 64 * (nbPoints + (1 << (c)))
			cost := float64(cc) / float64(c)
			if cost < min {
				min = cost
				C = c
			}
		}
		// empirical, needs to be tuned.
		// if C > 16 && nbPoints < 1 << 23 {
		// 	C = 16
		// }
		return C
	}

	var C uint64
	nbSplits := 1
	nbChunks := 0
	for nbChunks < config.NbTasks {
		C = bestC(nbPoints)
		nbChunks = int(fr.Limbs * 64 / C) // number of c-bit radixes in a scalar
		if (fr.Limbs*64)%C != 0 {
			nbChunks++
		}
		nbChunks *= nbSplits
		if nbChunks < config.NbTasks {
			nbSplits <<= 1
			nbPoints >>= 1
		}
	}

	// partition the scalars
	// note: we do that before the actual chunk processing, as for each c-bit window (starting from LSW)
	// if it's larger than 2^{c-1}, we have a carry we need to propagate up to the higher window
	var smallValues int
	scalars, smallValues = partitionScalars(scalars, C, config.ScalarsMont, config.NbTasks)

	// if we have more than 10% of small values, we split the processing of the first chunk in 2
	// we may want to do that in msmInnerG1Jac , but that would incur a cost of looping through all scalars one more time
	splitFirstChunk := (float64(smallValues) / float64(len(scalars))) >= 0.1

	// we have nbSplits intermediate results that we must sum together.
	_p := make([]G1Jac, nbSplits-1)
	chDone := make(chan int, nbSplits-1)
	for i := 0; i < nbSplits-1; i++ {
		start := i * nbPoints
		end := start + nbPoints
		go func(start, end, i int) {
			msmInnerG1Jac(&_p[i], int(C), points[start:end], scalars[start:end], splitFirstChunk)
			chDone <- i
		}(start, end, i)
	}

	msmInnerG1Jac(p, int(C), points[(nbSplits-1)*nbPoints:], scalars[(nbSplits-1)*nbPoints:], splitFirstChunk)
	for i := 0; i < nbSplits-1; i++ {
		done := <-chDone
		p.AddAssign(&_p[done])
	}
	close(chDone)
	return p, nil
}

func msmInnerG1Jac(p *G1Jac, c int, points []G1Affine, scalars []fr.Element, splitFirstChunk bool) {

	switch c {

	case 1:
		msmCG1Affine[bucketg1JacExtendedC1, bucketg1JacExtendedC1](p, 1, points, scalars, splitFirstChunk)

	case 2:
		msmCG1Affine[bucketg1JacExtendedC2, bucketg1JacExtendedC2](p, 2, points, scalars, splitFirstChunk)

	case 3:
		msmCG1Affine[bucketg1JacExtendedC3, bucketg1JacExtendedC1](p, 3, points, scalars, splitFirstChunk)

	case 4:
		msmCG1Affine[bucketg1JacExtendedC4, bucketg1JacExtendedC4](p, 4, points, scalars, splitFirstChunk)

	case 5:
		msmCG1Affine[bucketg1JacExtendedC5, bucketg1JacExtendedC1](p, 5, points, scalars, splitFirstChunk)

	case 6:
		msmCG1Affine[bucketg1JacExtendedC6, bucketg1JacExtendedC4](p, 6, points, scalars, splitFirstChunk)

	case 7:
		msmCG1Affine[bucketg1JacExtendedC7, bucketg1JacExtendedC4](p, 7, points, scalars, splitFirstChunk)

	case 8:
		msmCG1Affine[bucketg1JacExtendedC8, bucketg1JacExtendedC8](p, 8, points, scalars, splitFirstChunk)

	case 9:
		msmCG1Affine[bucketg1JacExtendedC9, bucketg1JacExtendedC4](p, 9, points, scalars, splitFirstChunk)

	case 10:
		msmCG1Affine[bucketg1JacExtendedC10, bucketg1JacExtendedC6](p, 10, points, scalars, splitFirstChunk)

	case 11:
		msmCG1Affine[bucketg1JacExtendedC11, bucketg1JacExtendedC3](p, 11, points, scalars, splitFirstChunk)

	case 12:
		msmCG1Affine[bucketg1JacExtendedC12, bucketg1JacExtendedC4](p, 12, points, scalars, splitFirstChunk)

	case 13:
		msmCG1Affine[bucketg1JacExtendedC13, bucketg1JacExtendedC9](p, 13, points, scalars, splitFirstChunk)

	case 14:
		msmCG1Affine[bucketg1JacExtendedC14, bucketg1JacExtendedC4](p, 14, points, scalars, splitFirstChunk)

	case 15:
		msmCG1Affine[bucketg1JacExtendedC15, bucketg1JacExtendedC1](p, 15, points, scalars, splitFirstChunk)

	case 16:
		msmCG1Affine[bucketg1JacExtendedC16, bucketg1JacExtendedC16](p, 16, points, scalars, splitFirstChunk)

	case 17:
		msmCG1Affine[bucketg1JacExtendedC17, bucketg1JacExtendedC1](p, 17, points, scalars, splitFirstChunk)

	case 18:
		msmCG1Affine[bucketg1JacExtendedC18, bucketg1JacExtendedC4](p, 18, points, scalars, splitFirstChunk)

	case 19:
		msmCG1Affine[bucketg1JacExtendedC19, bucketg1JacExtendedC9](p, 19, points, scalars, splitFirstChunk)

	case 20:
		msmCG1Affine[bucketg1JacExtendedC20, bucketg1JacExtendedC16](p, 20, points, scalars, splitFirstChunk)

	case 21:
		msmCG1Affine[bucketg1JacExtendedC21, bucketg1JacExtendedC4](p, 21, points, scalars, splitFirstChunk)

	case 22:
		msmCG1Affine[bucketg1JacExtendedC22, bucketg1JacExtendedC14](p, 22, points, scalars, splitFirstChunk)

	case 23:
		msmCG1Affine[bucketg1JacExtendedC23, bucketg1JacExtendedC3](p, 23, points, scalars, splitFirstChunk)

	default:
		panic("not implemented")
	}
}

// msmReduceChunkG1Affine reduces the weighted sum of the buckets into the result of the multiExp
func msmReduceChunkG1Affine(p *G1Jac, c int, chChunks []chan g1JacExtended) *G1Jac {
	var _p g1JacExtended
	totalj := <-chChunks[len(chChunks)-1]
	_p.Set(&totalj)
	for j := len(chChunks) - 2; j >= 0; j-- {
		for l := 0; l < c; l++ {
			_p.double(&_p)
		}
		totalj := <-chChunks[j]
		_p.add(&totalj)
	}

	return p.unsafeFromJacExtended(&_p)
}

func msmProcessChunkG1Affine[B ibg1JacExtended](chunk uint64,
	chRes chan<- g1JacExtended,
	c uint64,
	points []G1Affine,
	scalars []fr.Element) {

	mask := uint64((1 << c) - 1) // low c bits are 1
	msbWindow := uint64(1 << (c - 1))

	var buckets B
	for i := 0; i < len(buckets); i++ {
		buckets[i].setInfinity()
	}

	jc := uint64(chunk * c)
	s := selector{}
	s.index = jc / 64
	s.shift = jc - (s.index * 64)
	s.mask = mask << s.shift
	s.multiWordSelect = (64%c) != 0 && s.shift > (64-c) && s.index < (fr.Limbs-1)
	if s.multiWordSelect {
		nbBitsHigh := s.shift - uint64(64-c)
		s.maskHigh = (1 << nbBitsHigh) - 1
		s.shiftHigh = (c - nbBitsHigh)
	}

	// for each scalars, get the digit corresponding to the chunk we're processing.
	for i := 0; i < len(scalars); i++ {
		bits := (scalars[i][s.index] & s.mask) >> s.shift
		if s.multiWordSelect {
			bits += (scalars[i][s.index+1] & s.maskHigh) << s.shiftHigh
		}

		if bits == 0 {
			continue
		}

		// if msbWindow bit is set, we need to substract
		if bits&msbWindow == 0 {
			// add
			buckets[bits-1].addMixed(&points[i])
		} else {
			// sub
			buckets[bits & ^msbWindow].subMixed(&points[i])
		}
	}

	// reduce buckets into total
	// total =  bucket[0] + 2*bucket[1] + 3*bucket[2] ... + n*bucket[n-1]

	var runningSum, total g1JacExtended
	runningSum.setInfinity()
	total.setInfinity()
	for k := len(buckets) - 1; k >= 0; k-- {
		if !buckets[k].ZZ.IsZero() {
			runningSum.add(&buckets[k])
		}
		total.add(&runningSum)
	}

	chRes <- total

}

func msmCG1Affine[B ibg1JacExtended, LB ibg1JacExtended](p *G1Jac, c uint64, points []G1Affine, scalars []fr.Element, splitFirstChunk bool) *G1Jac {
	nbChunks := (fr.Limbs * 64 / c) // number of c-bit radixes in a scalar
	if (fr.Limbs*64)%c != 0 {
		nbChunks++
	}
	// for each chunk, spawn one go routine that'll loop through all the scalars in the
	// corresponding bit-window
	// note that buckets is an array allocated on the stack (for most sizes of c) and this is
	// critical for performance

	// each go routine sends its result in chChunks[i] channel
	chChunks := make([]chan g1JacExtended, nbChunks)
	for i := 0; i < len(chChunks); i++ {
		chChunks[i] = make(chan g1JacExtended, 1)
	}

	if (fr.Limbs*64)%c != 0 {
		// TODO @gbotrel not always needed to do ext jac here.
		go func(j uint64, points []G1Affine, scalars []fr.Element) {
			// var buckets LB
			// lastC := (fr.Limbs * 64) - (c * (fr.Limbs * 64 / c))
			// buckets := make([]g1JacExtended, 1<<(lastC-1))
			// TODO @gbotrel last C restore.
			msmProcessChunkG1Affine[LB](j, chChunks[j], c, points, scalars)
		}(uint64(nbChunks-1), points, scalars)
		nbChunks--
	}

	processChunk := func(j int, points []G1Affine, scalars []fr.Element, chChunk chan g1JacExtended) {
		msmProcessChunkG1Affine[B](uint64(j), chChunk, c, points, scalars)
	}

	for j := int(nbChunks - 1); j > 0; j-- {
		go processChunk(j, points, scalars, chChunks[j])
	}

	if !splitFirstChunk {
		go processChunk(0, points, scalars, chChunks[0])
	} else {
		chSplit := make(chan g1JacExtended, 2)
		split := len(points) / 2
		go processChunk(0, points[:split], scalars[:split], chSplit)
		go processChunk(0, points[split:], scalars[split:], chSplit)
		go func() {
			s1 := <-chSplit
			s2 := <-chSplit
			close(chSplit)
			s1.add(&s2)
			chChunks[0] <- s1
		}()
	}

	return msmReduceChunkG1Affine(p, int(c), chChunks[:])
}

// MultiExp implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G2Affine) MultiExp(points []G2Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G2Affine, error) {
	var _p G2Jac
	if _, err := _p.MultiExp(points, scalars, config); err != nil {
		return nil, err
	}
	p.FromJacobian(&_p)
	return p, nil
}

// MultiExp implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G2Jac) MultiExp(points []G2Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G2Jac, error) {
	// note:
	// each of the msmCX method is the same, except for the c constant it declares
	// duplicating (through template generation) these methods allows to declare the buckets on the stack
	// the choice of c needs to be improved:
	// there is a theoritical value that gives optimal asymptotics
	// but in practice, other factors come into play, including:
	// * if c doesn't divide 64, the word size, then we're bound to select bits over 2 words of our scalars, instead of 1
	// * number of CPUs
	// * cache friendliness (which depends on the host, G1 or G2... )
	//	--> for example, on BN254, a G1 point fits into one cache line of 64bytes, but a G2 point don't.

	// for each msmCX
	// step 1
	// we compute, for each scalars over c-bit wide windows, nbChunk digits
	// if the digit is larger than 2^{c-1}, then, we borrow 2^c from the next window and substract
	// 2^{c} to the current digit, making it negative.
	// negative digits will be processed in the next step as adding -G into the bucket instead of G
	// (computing -G is cheap, and this saves us half of the buckets)
	// step 2
	// buckets are declared on the stack
	// notice that we have 2^{c-1} buckets instead of 2^{c} (see step1)
	// we use jacobian extended formulas here as they are faster than mixed addition
	// msmProcessChunk places points into buckets base on their selector and return the weighted bucket sum in given channel
	// step 3
	// reduce the buckets weigthed sums into our result (msmReduceChunk)

	// ensure len(points) == len(scalars)
	nbPoints := len(points)
	if nbPoints != len(scalars) {
		return nil, errors.New("len(points) != len(scalars)")
	}

	// if nbTasks is not set, use all available CPUs
	if config.NbTasks <= 0 {
		config.NbTasks = runtime.NumCPU()
	} else if config.NbTasks > 1024 {
		return nil, errors.New("invalid config: config.NbTasks > 1024")
	}

	// here, we compute the best C for nbPoints
	// we split recursively until nbChunks(c) >= nbTasks,
	bestC := func(nbPoints int) uint64 {
		// implemented msmC methods (the c we use must be in this slice)
		implementedCs := []uint64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23}
		var C uint64
		// approximate cost (in group operations)
		// cost = bits/c * (nbPoints + 2^{c})
		// this needs to be verified empirically.
		// for example, on a MBP 2016, for G2 MultiExp > 8M points, hand picking c gives better results
		min := math.MaxFloat64
		for _, c := range implementedCs {
			cc := fr.Limbs * 64 * (nbPoints + (1 << (c)))
			cost := float64(cc) / float64(c)
			if cost < min {
				min = cost
				C = c
			}
		}
		// empirical, needs to be tuned.
		// if C > 16 && nbPoints < 1 << 23 {
		// 	C = 16
		// }
		return C
	}

	var C uint64
	nbSplits := 1
	nbChunks := 0
	for nbChunks < config.NbTasks {
		C = bestC(nbPoints)
		nbChunks = int(fr.Limbs * 64 / C) // number of c-bit radixes in a scalar
		if (fr.Limbs*64)%C != 0 {
			nbChunks++
		}
		nbChunks *= nbSplits
		if nbChunks < config.NbTasks {
			nbSplits <<= 1
			nbPoints >>= 1
		}
	}

	// partition the scalars
	// note: we do that before the actual chunk processing, as for each c-bit window (starting from LSW)
	// if it's larger than 2^{c-1}, we have a carry we need to propagate up to the higher window
	var smallValues int
	scalars, smallValues = partitionScalars(scalars, C, config.ScalarsMont, config.NbTasks)

	// if we have more than 10% of small values, we split the processing of the first chunk in 2
	// we may want to do that in msmInnerG2Jac , but that would incur a cost of looping through all scalars one more time
	splitFirstChunk := (float64(smallValues) / float64(len(scalars))) >= 0.1

	// we have nbSplits intermediate results that we must sum together.
	_p := make([]G2Jac, nbSplits-1)
	chDone := make(chan int, nbSplits-1)
	for i := 0; i < nbSplits-1; i++ {
		start := i * nbPoints
		end := start + nbPoints
		go func(start, end, i int) {
			msmInnerG2Jac(&_p[i], int(C), points[start:end], scalars[start:end], splitFirstChunk)
			chDone <- i
		}(start, end, i)
	}

	msmInnerG2Jac(p, int(C), points[(nbSplits-1)*nbPoints:], scalars[(nbSplits-1)*nbPoints:], splitFirstChunk)
	for i := 0; i < nbSplits-1; i++ {
		done := <-chDone
		p.AddAssign(&_p[done])
	}
	close(chDone)
	return p, nil
}

func msmInnerG2Jac(p *G2Jac, c int, points []G2Affine, scalars []fr.Element, splitFirstChunk bool) {

	switch c {

	case 1:
		msmCG2Affine[bucketg2JacExtendedC1, bucketg2JacExtendedC1](p, 1, points, scalars, splitFirstChunk)

	case 2:
		msmCG2Affine[bucketg2JacExtendedC2, bucketg2JacExtendedC2](p, 2, points, scalars, splitFirstChunk)

	case 3:
		msmCG2Affine[bucketg2JacExtendedC3, bucketg2JacExtendedC1](p, 3, points, scalars, splitFirstChunk)

	case 4:
		msmCG2Affine[bucketg2JacExtendedC4, bucketg2JacExtendedC4](p, 4, points, scalars, splitFirstChunk)

	case 5:
		msmCG2Affine[bucketg2JacExtendedC5, bucketg2JacExtendedC1](p, 5, points, scalars, splitFirstChunk)

	case 6:
		msmCG2Affine[bucketg2JacExtendedC6, bucketg2JacExtendedC4](p, 6, points, scalars, splitFirstChunk)

	case 7:
		msmCG2Affine[bucketg2JacExtendedC7, bucketg2JacExtendedC4](p, 7, points, scalars, splitFirstChunk)

	case 8:
		msmCG2Affine[bucketg2JacExtendedC8, bucketg2JacExtendedC8](p, 8, points, scalars, splitFirstChunk)

	case 9:
		msmCG2Affine[bucketg2JacExtendedC9, bucketg2JacExtendedC4](p, 9, points, scalars, splitFirstChunk)

	case 10:
		msmCG2Affine[bucketg2JacExtendedC10, bucketg2JacExtendedC6](p, 10, points, scalars, splitFirstChunk)

	case 11:
		msmCG2Affine[bucketg2JacExtendedC11, bucketg2JacExtendedC3](p, 11, points, scalars, splitFirstChunk)

	case 12:
		msmCG2Affine[bucketg2JacExtendedC12, bucketg2JacExtendedC4](p, 12, points, scalars, splitFirstChunk)

	case 13:
		msmCG2Affine[bucketg2JacExtendedC13, bucketg2JacExtendedC9](p, 13, points, scalars, splitFirstChunk)

	case 14:
		msmCG2Affine[bucketg2JacExtendedC14, bucketg2JacExtendedC4](p, 14, points, scalars, splitFirstChunk)

	case 15:
		msmCG2Affine[bucketg2JacExtendedC15, bucketg2JacExtendedC1](p, 15, points, scalars, splitFirstChunk)

	case 16:
		msmCG2Affine[bucketg2JacExtendedC16, bucketg2JacExtendedC16](p, 16, points, scalars, splitFirstChunk)

	case 17:
		msmCG2Affine[bucketg2JacExtendedC17, bucketg2JacExtendedC1](p, 17, points, scalars, splitFirstChunk)

	case 18:
		msmCG2Affine[bucketg2JacExtendedC18, bucketg2JacExtendedC4](p, 18, points, scalars, splitFirstChunk)

	case 19:
		msmCG2Affine[bucketg2JacExtendedC19, bucketg2JacExtendedC9](p, 19, points, scalars, splitFirstChunk)

	case 20:
		msmCG2Affine[bucketg2JacExtendedC20, bucketg2JacExtendedC16](p, 20, points, scalars, splitFirstChunk)

	case 21:
		msmCG2Affine[bucketg2JacExtendedC21, bucketg2JacExtendedC4](p, 21, points, scalars, splitFirstChunk)

	case 22:
		msmCG2Affine[bucketg2JacExtendedC22, bucketg2JacExtendedC14](p, 22, points, scalars, splitFirstChunk)

	case 23:
		msmCG2Affine[bucketg2JacExtendedC23, bucketg2JacExtendedC3](p, 23, points, scalars, splitFirstChunk)

	default:
		panic("not implemented")
	}
}

// msmReduceChunkG2Affine reduces the weighted sum of the buckets into the result of the multiExp
func msmReduceChunkG2Affine(p *G2Jac, c int, chChunks []chan g2JacExtended) *G2Jac {
	var _p g2JacExtended
	totalj := <-chChunks[len(chChunks)-1]
	_p.Set(&totalj)
	for j := len(chChunks) - 2; j >= 0; j-- {
		for l := 0; l < c; l++ {
			_p.double(&_p)
		}
		totalj := <-chChunks[j]
		_p.add(&totalj)
	}

	return p.unsafeFromJacExtended(&_p)
}

func msmProcessChunkG2Affine[B ibg2JacExtended](chunk uint64,
	chRes chan<- g2JacExtended,
	c uint64,
	points []G2Affine,
	scalars []fr.Element) {

	mask := uint64((1 << c) - 1) // low c bits are 1
	msbWindow := uint64(1 << (c - 1))

	var buckets B
	for i := 0; i < len(buckets); i++ {
		buckets[i].setInfinity()
	}

	jc := uint64(chunk * c)
	s := selector{}
	s.index = jc / 64
	s.shift = jc - (s.index * 64)
	s.mask = mask << s.shift
	s.multiWordSelect = (64%c) != 0 && s.shift > (64-c) && s.index < (fr.Limbs-1)
	if s.multiWordSelect {
		nbBitsHigh := s.shift - uint64(64-c)
		s.maskHigh = (1 << nbBitsHigh) - 1
		s.shiftHigh = (c - nbBitsHigh)
	}

	// for each scalars, get the digit corresponding to the chunk we're processing.
	for i := 0; i < len(scalars); i++ {
		bits := (scalars[i][s.index] & s.mask) >> s.shift
		if s.multiWordSelect {
			bits += (scalars[i][s.index+1] & s.maskHigh) << s.shiftHigh
		}

		if bits == 0 {
			continue
		}

		// if msbWindow bit is set, we need to substract
		if bits&msbWindow == 0 {
			// add
			buckets[bits-1].addMixed(&points[i])
		} else {
			// sub
			buckets[bits & ^msbWindow].subMixed(&points[i])
		}
	}

	// reduce buckets into total
	// total =  bucket[0] + 2*bucket[1] + 3*bucket[2] ... + n*bucket[n-1]

	var runningSum, total g2JacExtended
	runningSum.setInfinity()
	total.setInfinity()
	for k := len(buckets) - 1; k >= 0; k-- {
		if !buckets[k].ZZ.IsZero() {
			runningSum.add(&buckets[k])
		}
		total.add(&runningSum)
	}

	chRes <- total

}

func msmCG2Affine[B ibg2JacExtended, LB ibg2JacExtended](p *G2Jac, c uint64, points []G2Affine, scalars []fr.Element, splitFirstChunk bool) *G2Jac {
	nbChunks := (fr.Limbs * 64 / c) // number of c-bit radixes in a scalar
	if (fr.Limbs*64)%c != 0 {
		nbChunks++
	}
	// for each chunk, spawn one go routine that'll loop through all the scalars in the
	// corresponding bit-window
	// note that buckets is an array allocated on the stack (for most sizes of c) and this is
	// critical for performance

	// each go routine sends its result in chChunks[i] channel
	chChunks := make([]chan g2JacExtended, nbChunks)
	for i := 0; i < len(chChunks); i++ {
		chChunks[i] = make(chan g2JacExtended, 1)
	}

	if (fr.Limbs*64)%c != 0 {
		// TODO @gbotrel not always needed to do ext jac here.
		go func(j uint64, points []G2Affine, scalars []fr.Element) {
			// var buckets LB
			// lastC := (fr.Limbs * 64) - (c * (fr.Limbs * 64 / c))
			// buckets := make([]g2JacExtended, 1<<(lastC-1))
			// TODO @gbotrel last C restore.
			msmProcessChunkG2Affine[LB](j, chChunks[j], c, points, scalars)
		}(uint64(nbChunks-1), points, scalars)
		nbChunks--
	}

	processChunk := func(j int, points []G2Affine, scalars []fr.Element, chChunk chan g2JacExtended) {
		msmProcessChunkG2Affine[B](uint64(j), chChunk, c, points, scalars)
	}

	for j := int(nbChunks - 1); j > 0; j-- {
		go processChunk(j, points, scalars, chChunks[j])
	}

	if !splitFirstChunk {
		go processChunk(0, points, scalars, chChunks[0])
	} else {
		chSplit := make(chan g2JacExtended, 2)
		split := len(points) / 2
		go processChunk(0, points[:split], scalars[:split], chSplit)
		go processChunk(0, points[split:], scalars[split:], chSplit)
		go func() {
			s1 := <-chSplit
			s2 := <-chSplit
			close(chSplit)
			s1.add(&s2)
			chChunks[0] <- s1
		}()
	}

	return msmReduceChunkG2Affine(p, int(c), chChunks[:])
}
