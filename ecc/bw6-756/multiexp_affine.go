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

package bw6756

import (
	"errors"
	"github.com/consensys/gnark-crypto/ecc"
	"github.com/consensys/gnark-crypto/ecc/bw6-756/fr"
	"math"
	"runtime"
)

const MAX_BATCH_SIZE = 600

type batchOp struct {
	bucketID, pointID uint32
}

func (o batchOp) isNeg() bool {
	return o.pointID&1 == 1
}

// MultiExpBatchAffine implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G1Affine) MultiExpBatchAffine(points []G1Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G1Affine, error) {
	var _p G1Jac
	if _, err := _p.MultiExpBatchAffine(points, scalars, config); err != nil {
		return nil, err
	}
	p.FromJacobian(&_p)
	return p, nil
}

// MultiExpBatchAffine implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G1Jac) MultiExpBatchAffine(points []G1Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G1Jac, error) {
	// note:
	// each of the batchAffineMsmCX method is the same, except for the c constant it declares
	// duplicating (through template generation) these methods allows to declare the buckets on the stack
	// the choice of c needs to be improved:
	// there is a theoritical value that gives optimal asymptotics
	// but in practice, other factors come into play, including:
	// * if c doesn't divide 64, the word size, then we're bound to select bits over 2 words of our scalars, instead of 1
	// * number of CPUs
	// * cache friendliness (which depends on the host, G1 or G2... )
	//	--> for example, on BN254, a G1 point fits into one cache line of 64bytes, but a G2 point don't.

	// for each batchAffineMsmCX
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
		// implemented batchAffineMsmC methods (the c we use must be in this slice)
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
	// we may want to do that in msmInnerG1JacBatchAffine , but that would incur a cost of looping through all scalars one more time
	splitFirstChunk := (float64(smallValues) / float64(len(scalars))) >= 0.1

	// we have nbSplits intermediate results that we must sum together.
	_p := make([]G1Jac, nbSplits-1)
	chDone := make(chan int, nbSplits-1)
	for i := 0; i < nbSplits-1; i++ {
		start := i * nbPoints
		end := start + nbPoints
		go func(start, end, i int) {
			msmInnerG1JacBatchAffine(&_p[i], int(C), points[start:end], scalars[start:end], splitFirstChunk)
			chDone <- i
		}(start, end, i)
	}

	msmInnerG1JacBatchAffine(p, int(C), points[(nbSplits-1)*nbPoints:], scalars[(nbSplits-1)*nbPoints:], splitFirstChunk)
	for i := 0; i < nbSplits-1; i++ {
		done := <-chDone
		p.AddAssign(&_p[done])
	}
	close(chDone)
	return p, nil
}

func msmInnerG1JacBatchAffine(p *G1Jac, c int, points []G1Affine, scalars []fr.Element, splitFirstChunk bool) {

	switch c {

	case 1:
		msmCG1Affine[bucketg1JacExtendedC1, bucketg1JacExtendedC1](p, 1, points, scalars, splitFirstChunk)

	case 2:
		msmCG1Affine[bucketg1JacExtendedC2, bucketg1JacExtendedC2](p, 2, points, scalars, splitFirstChunk)

	case 3:
		msmCG1Affine[bucketg1JacExtendedC3, bucketg1JacExtendedC3](p, 3, points, scalars, splitFirstChunk)

	case 4:
		msmCG1Affine[bucketg1JacExtendedC4, bucketg1JacExtendedC4](p, 4, points, scalars, splitFirstChunk)

	case 5:
		msmCG1Affine[bucketg1JacExtendedC5, bucketg1JacExtendedC4](p, 5, points, scalars, splitFirstChunk)

	case 6:
		msmCG1Affine[bucketg1JacExtendedC6, bucketg1JacExtendedC6](p, 6, points, scalars, splitFirstChunk)

	case 7:
		msmCG1Affine[bucketg1JacExtendedC7, bucketg1JacExtendedC6](p, 7, points, scalars, splitFirstChunk)

	case 8:
		msmCG1Affine[bucketg1JacExtendedC8, bucketg1JacExtendedC8](p, 8, points, scalars, splitFirstChunk)

	case 9:
		msmCG1Affine[bucketg1JacExtendedC9, bucketg1JacExtendedC6](p, 9, points, scalars, splitFirstChunk)

	case 10:
		batchG1AffineMsm[bucketG1AffineC10, bucketg1JacExtendedC4](p, 10, points, scalars, splitFirstChunk)

	case 11:
		batchG1AffineMsm[bucketG1AffineC11, bucketg1JacExtendedC10](p, 11, points, scalars, splitFirstChunk)

	case 12:
		batchG1AffineMsm[bucketG1AffineC12, bucketg1JacExtendedC12](p, 12, points, scalars, splitFirstChunk)

	case 13:
		batchG1AffineMsm[bucketG1AffineC13, bucketg1JacExtendedC7](p, 13, points, scalars, splitFirstChunk)

	case 14:
		batchG1AffineMsm[bucketG1AffineC14, bucketg1JacExtendedC6](p, 14, points, scalars, splitFirstChunk)

	case 15:
		batchG1AffineMsm[bucketG1AffineC15, bucketg1JacExtendedC9](p, 15, points, scalars, splitFirstChunk)

	case 16:
		batchG1AffineMsm[bucketG1AffineC16, bucketg1JacExtendedC16](p, 16, points, scalars, splitFirstChunk)

	case 17:
		batchG1AffineMsm[bucketG1AffineC17, bucketg1JacExtendedC10](p, 17, points, scalars, splitFirstChunk)

	case 18:
		batchG1AffineMsm[bucketG1AffineC18, bucketg1JacExtendedC6](p, 18, points, scalars, splitFirstChunk)

	case 19:
		batchG1AffineMsm[bucketG1AffineC19, bucketg1JacExtendedC4](p, 19, points, scalars, splitFirstChunk)

	case 20:
		batchG1AffineMsm[bucketG1AffineC20, bucketg1JacExtendedC4](p, 20, points, scalars, splitFirstChunk)

	case 21:
		batchG1AffineMsm[bucketG1AffineC21, bucketg1JacExtendedC6](p, 21, points, scalars, splitFirstChunk)

	case 22:
		batchG1AffineMsm[bucketG1AffineC22, bucketg1JacExtendedC10](p, 22, points, scalars, splitFirstChunk)

	case 23:
		batchG1AffineMsm[bucketG1AffineC23, bucketg1JacExtendedC16](p, 23, points, scalars, splitFirstChunk)

	default:
		panic("not implemented")
	}
}

type BatchG1Affine[B ibG1Affine] struct {
	P         [MAX_BATCH_SIZE]G1Affine
	R         [MAX_BATCH_SIZE]*G1Affine
	batchSize int
	cptP      int
	bucketIds map[uint32]struct{}
	points    []G1Affine
	buckets   *B
}

func newBatchG1Affine[B ibG1Affine](buckets *B, points []G1Affine) BatchG1Affine[B] {
	batchSize := len(*buckets) / 5
	if batchSize > MAX_BATCH_SIZE {
		batchSize = MAX_BATCH_SIZE
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	return BatchG1Affine[B]{
		buckets:   buckets,
		points:    points,
		batchSize: batchSize,
		bucketIds: make(map[uint32]struct{}, len(*buckets)/2),
	}
}

func (b *BatchG1Affine[B]) IsFull() bool {
	return b.cptP == b.batchSize
}

func (b *BatchG1Affine[B]) ExecuteAndReset() {
	if b.cptP == 0 {
		return
	}
	// for i := 0; i < len(b.R); i++ {
	// 	b.R[i].Add(b.R[i], b.P[i])
	// }
	BatchAddG1Affine(b.R[:b.cptP], b.P[:b.cptP], b.cptP)
	for k := range b.bucketIds {
		delete(b.bucketIds, k)
	}
	// b.bucketIds = [MAX_BATCH_SIZE]uint32{}
	b.cptP = 0
}

func (b *BatchG1Affine[B]) CanAdd(bID uint32) bool {
	_, ok := b.bucketIds[bID]
	return !ok
}

func (b *BatchG1Affine[B]) Add(op batchOp) {
	// CanAdd must be called before --> ensures bucket is not "used" in current batch

	BK := &(*b.buckets)[op.bucketID]
	P := &b.points[op.pointID>>1]
	if P.IsInfinity() {
		return
	}
	// handle special cases with inf or -P / P
	if BK.IsInfinity() {
		if op.isNeg() {
			BK.Neg(P)
		} else {
			BK.Set(P)
		}
		return
	}
	if op.isNeg() {
		// if bucket == P --> -P == 0
		if BK.Equal(P) {
			BK.setInfinity()
			return
		}
	} else {
		// if bucket == -P, B == 0
		if BK.X.Equal(&P.X) && !BK.Y.Equal(&P.Y) {
			BK.setInfinity()
			return
		}
	}

	// b.bucketIds[b.cptP] = op.bucketID
	b.bucketIds[op.bucketID] = struct{}{}
	b.R[b.cptP] = BK
	if op.isNeg() {
		b.P[b.cptP].Neg(P)
	} else {
		b.P[b.cptP].Set(P)
	}
	b.cptP++
}

func processQueueG1Affine[B ibG1Affine](queue []batchOp, batch *BatchG1Affine[B]) []batchOp {
	for i := len(queue) - 1; i >= 0; i-- {
		if batch.CanAdd(queue[i].bucketID) {
			batch.Add(queue[i])
			if batch.IsFull() {
				batch.ExecuteAndReset()
			}
			queue[i] = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		}
	}
	return queue

}

func msmProcessChunkG1AffineBatchAffine[B ibG1Affine](chunk uint64,
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

	batch := newBatchG1Affine(&buckets, points)
	queue := make([]batchOp, 0, 4096) // TODO find right capacity here.
	nbBatches := 0
	for i := 0; i < len(scalars); i++ {
		bits := (scalars[i][s.index] & s.mask) >> s.shift
		if s.multiWordSelect {
			bits += (scalars[i][s.index+1] & s.maskHigh) << s.shiftHigh
		}

		if bits == 0 {
			continue
		}

		op := batchOp{pointID: uint32(i) << 1}
		// if msbWindow bit is set, we need to substract
		if bits&msbWindow == 0 {
			// add
			op.bucketID = uint32(bits - 1)
			// buckets[bits-1].Add(&points[i], &buckets[bits-1])
		} else {
			// sub
			op.bucketID = (uint32(bits & ^msbWindow))
			op.pointID += 1
			// op.isNeg = true
			// buckets[bits & ^msbWindow].Sub( &buckets[bits & ^msbWindow], &points[i])
		}
		if batch.CanAdd(op.bucketID) {
			batch.Add(op)
			if batch.IsFull() {
				batch.ExecuteAndReset()
				nbBatches++
				if len(queue) != 0 { // TODO @gbotrel this doesn't seem to help much? should minimize queue resizing
					batch.Add(queue[len(queue)-1])
					queue = queue[:len(queue)-1]
				}
			}
		} else {
			// put it in queue.
			queue = append(queue, op)
		}
	}
	// fmt.Printf("chunk %d\nlen(queue)=%d\nnbBatches=%d\nbatchSize=%d\nnbBuckets=%d\nnbPoints=%d\n",
	// 	chunk, len(queue), nbBatches, batch.batchSize, len(buckets), len(points))
	// batch.ExecuteAndReset()
	for len(queue) != 0 {
		queue = processQueueG1Affine(queue, &batch)
		batch.ExecuteAndReset() // execute batch even if not full.
	}

	// flush items in batch.
	batch.ExecuteAndReset()

	// reduce buckets into total
	// total =  bucket[0] + 2*bucket[1] + 3*bucket[2] ... + n*bucket[n-1]

	var runningSum, total g1JacExtended
	runningSum.setInfinity()
	total.setInfinity()
	for k := len(buckets) - 1; k >= 0; k-- {
		if !buckets[k].IsInfinity() {
			runningSum.addMixed(&buckets[k])
		}
		total.add(&runningSum)
	}

	chRes <- total

}

func batchG1AffineMsm[B ibG1Affine, J ibg1JacExtended](p *G1Jac, c uint64, points []G1Affine, scalars []fr.Element, splitFirstChunk bool) *G1Jac {

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
			// TODO @gbotrel lastC restore.
			msmProcessChunkG1Affine[J](j, chChunks[j], c, points, scalars)
		}(uint64(nbChunks-1), points, scalars)
		nbChunks--
	}

	processChunk := func(j int, points []G1Affine, scalars []fr.Element, chChunk chan g1JacExtended) {
		msmProcessChunkG1AffineBatchAffine[B](uint64(j), chChunk, c, points, scalars)
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

type bucketG1AffineC1 [1 << (1 - 1)]G1Affine
type bucketG1AffineC2 [1 << (2 - 1)]G1Affine
type bucketG1AffineC3 [1 << (3 - 1)]G1Affine
type bucketG1AffineC4 [1 << (4 - 1)]G1Affine
type bucketG1AffineC5 [1 << (5 - 1)]G1Affine
type bucketG1AffineC6 [1 << (6 - 1)]G1Affine
type bucketG1AffineC7 [1 << (7 - 1)]G1Affine
type bucketG1AffineC8 [1 << (8 - 1)]G1Affine
type bucketG1AffineC9 [1 << (9 - 1)]G1Affine
type bucketG1AffineC10 [1 << (10 - 1)]G1Affine
type bucketG1AffineC11 [1 << (11 - 1)]G1Affine
type bucketG1AffineC12 [1 << (12 - 1)]G1Affine
type bucketG1AffineC13 [1 << (13 - 1)]G1Affine
type bucketG1AffineC14 [1 << (14 - 1)]G1Affine
type bucketG1AffineC15 [1 << (15 - 1)]G1Affine
type bucketG1AffineC16 [1 << (16 - 1)]G1Affine
type bucketG1AffineC17 [1 << (17 - 1)]G1Affine
type bucketG1AffineC18 [1 << (18 - 1)]G1Affine
type bucketG1AffineC19 [1 << (19 - 1)]G1Affine
type bucketG1AffineC20 [1 << (20 - 1)]G1Affine
type bucketG1AffineC21 [1 << (21 - 1)]G1Affine
type bucketG1AffineC22 [1 << (22 - 1)]G1Affine
type bucketG1AffineC23 [1 << (23 - 1)]G1Affine
type bucketg1JacExtendedC1 [1 << (1 - 1)]g1JacExtended
type bucketg1JacExtendedC2 [1 << (2 - 1)]g1JacExtended
type bucketg1JacExtendedC3 [1 << (3 - 1)]g1JacExtended
type bucketg1JacExtendedC4 [1 << (4 - 1)]g1JacExtended
type bucketg1JacExtendedC5 [1 << (5 - 1)]g1JacExtended
type bucketg1JacExtendedC6 [1 << (6 - 1)]g1JacExtended
type bucketg1JacExtendedC7 [1 << (7 - 1)]g1JacExtended
type bucketg1JacExtendedC8 [1 << (8 - 1)]g1JacExtended
type bucketg1JacExtendedC9 [1 << (9 - 1)]g1JacExtended
type bucketg1JacExtendedC10 [1 << (10 - 1)]g1JacExtended
type bucketg1JacExtendedC11 [1 << (11 - 1)]g1JacExtended
type bucketg1JacExtendedC12 [1 << (12 - 1)]g1JacExtended
type bucketg1JacExtendedC13 [1 << (13 - 1)]g1JacExtended
type bucketg1JacExtendedC14 [1 << (14 - 1)]g1JacExtended
type bucketg1JacExtendedC15 [1 << (15 - 1)]g1JacExtended
type bucketg1JacExtendedC16 [1 << (16 - 1)]g1JacExtended
type bucketg1JacExtendedC17 [1 << (17 - 1)]g1JacExtended
type bucketg1JacExtendedC18 [1 << (18 - 1)]g1JacExtended
type bucketg1JacExtendedC19 [1 << (19 - 1)]g1JacExtended
type bucketg1JacExtendedC20 [1 << (20 - 1)]g1JacExtended
type bucketg1JacExtendedC21 [1 << (21 - 1)]g1JacExtended
type bucketg1JacExtendedC22 [1 << (22 - 1)]g1JacExtended
type bucketg1JacExtendedC23 [1 << (23 - 1)]g1JacExtended

type ibG1Affine interface {
	bucketG1AffineC1 |
		bucketG1AffineC2 |
		bucketG1AffineC3 |
		bucketG1AffineC4 |
		bucketG1AffineC5 |
		bucketG1AffineC6 |
		bucketG1AffineC7 |
		bucketG1AffineC8 |
		bucketG1AffineC9 |
		bucketG1AffineC10 |
		bucketG1AffineC11 |
		bucketG1AffineC12 |
		bucketG1AffineC13 |
		bucketG1AffineC14 |
		bucketG1AffineC15 |
		bucketG1AffineC16 |
		bucketG1AffineC17 |
		bucketG1AffineC18 |
		bucketG1AffineC19 |
		bucketG1AffineC20 |
		bucketG1AffineC21 |
		bucketG1AffineC22 |
		bucketG1AffineC23
}

type ibg1JacExtended interface {
	bucketg1JacExtendedC1 |
		bucketg1JacExtendedC2 |
		bucketg1JacExtendedC3 |
		bucketg1JacExtendedC4 |
		bucketg1JacExtendedC5 |
		bucketg1JacExtendedC6 |
		bucketg1JacExtendedC7 |
		bucketg1JacExtendedC8 |
		bucketg1JacExtendedC9 |
		bucketg1JacExtendedC10 |
		bucketg1JacExtendedC11 |
		bucketg1JacExtendedC12 |
		bucketg1JacExtendedC13 |
		bucketg1JacExtendedC14 |
		bucketg1JacExtendedC15 |
		bucketg1JacExtendedC16 |
		bucketg1JacExtendedC17 |
		bucketg1JacExtendedC18 |
		bucketg1JacExtendedC19 |
		bucketg1JacExtendedC20 |
		bucketg1JacExtendedC21 |
		bucketg1JacExtendedC22 |
		bucketg1JacExtendedC23
}

// MultiExpBatchAffine implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G2Affine) MultiExpBatchAffine(points []G2Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G2Affine, error) {
	var _p G2Jac
	if _, err := _p.MultiExpBatchAffine(points, scalars, config); err != nil {
		return nil, err
	}
	p.FromJacobian(&_p)
	return p, nil
}

// MultiExpBatchAffine implements section 4 of https://eprint.iacr.org/2012/549.pdf
//
// This call return an error if len(scalars) != len(points) or if provided config is invalid.
func (p *G2Jac) MultiExpBatchAffine(points []G2Affine, scalars []fr.Element, config ecc.MultiExpConfig) (*G2Jac, error) {
	// note:
	// each of the batchAffineMsmCX method is the same, except for the c constant it declares
	// duplicating (through template generation) these methods allows to declare the buckets on the stack
	// the choice of c needs to be improved:
	// there is a theoritical value that gives optimal asymptotics
	// but in practice, other factors come into play, including:
	// * if c doesn't divide 64, the word size, then we're bound to select bits over 2 words of our scalars, instead of 1
	// * number of CPUs
	// * cache friendliness (which depends on the host, G1 or G2... )
	//	--> for example, on BN254, a G1 point fits into one cache line of 64bytes, but a G2 point don't.

	// for each batchAffineMsmCX
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
		// implemented batchAffineMsmC methods (the c we use must be in this slice)
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
	// we may want to do that in msmInnerG2JacBatchAffine , but that would incur a cost of looping through all scalars one more time
	splitFirstChunk := (float64(smallValues) / float64(len(scalars))) >= 0.1

	// we have nbSplits intermediate results that we must sum together.
	_p := make([]G2Jac, nbSplits-1)
	chDone := make(chan int, nbSplits-1)
	for i := 0; i < nbSplits-1; i++ {
		start := i * nbPoints
		end := start + nbPoints
		go func(start, end, i int) {
			msmInnerG2JacBatchAffine(&_p[i], int(C), points[start:end], scalars[start:end], splitFirstChunk)
			chDone <- i
		}(start, end, i)
	}

	msmInnerG2JacBatchAffine(p, int(C), points[(nbSplits-1)*nbPoints:], scalars[(nbSplits-1)*nbPoints:], splitFirstChunk)
	for i := 0; i < nbSplits-1; i++ {
		done := <-chDone
		p.AddAssign(&_p[done])
	}
	close(chDone)
	return p, nil
}

func msmInnerG2JacBatchAffine(p *G2Jac, c int, points []G2Affine, scalars []fr.Element, splitFirstChunk bool) {

	switch c {

	case 1:
		msmCG2Affine[bucketg2JacExtendedC1, bucketg2JacExtendedC1](p, 1, points, scalars, splitFirstChunk)

	case 2:
		msmCG2Affine[bucketg2JacExtendedC2, bucketg2JacExtendedC2](p, 2, points, scalars, splitFirstChunk)

	case 3:
		msmCG2Affine[bucketg2JacExtendedC3, bucketg2JacExtendedC3](p, 3, points, scalars, splitFirstChunk)

	case 4:
		msmCG2Affine[bucketg2JacExtendedC4, bucketg2JacExtendedC4](p, 4, points, scalars, splitFirstChunk)

	case 5:
		msmCG2Affine[bucketg2JacExtendedC5, bucketg2JacExtendedC4](p, 5, points, scalars, splitFirstChunk)

	case 6:
		msmCG2Affine[bucketg2JacExtendedC6, bucketg2JacExtendedC6](p, 6, points, scalars, splitFirstChunk)

	case 7:
		msmCG2Affine[bucketg2JacExtendedC7, bucketg2JacExtendedC6](p, 7, points, scalars, splitFirstChunk)

	case 8:
		msmCG2Affine[bucketg2JacExtendedC8, bucketg2JacExtendedC8](p, 8, points, scalars, splitFirstChunk)

	case 9:
		msmCG2Affine[bucketg2JacExtendedC9, bucketg2JacExtendedC6](p, 9, points, scalars, splitFirstChunk)

	case 10:
		batchG2AffineMsm[bucketG2AffineC10, bucketg2JacExtendedC4](p, 10, points, scalars, splitFirstChunk)

	case 11:
		batchG2AffineMsm[bucketG2AffineC11, bucketg2JacExtendedC10](p, 11, points, scalars, splitFirstChunk)

	case 12:
		batchG2AffineMsm[bucketG2AffineC12, bucketg2JacExtendedC12](p, 12, points, scalars, splitFirstChunk)

	case 13:
		batchG2AffineMsm[bucketG2AffineC13, bucketg2JacExtendedC7](p, 13, points, scalars, splitFirstChunk)

	case 14:
		batchG2AffineMsm[bucketG2AffineC14, bucketg2JacExtendedC6](p, 14, points, scalars, splitFirstChunk)

	case 15:
		batchG2AffineMsm[bucketG2AffineC15, bucketg2JacExtendedC9](p, 15, points, scalars, splitFirstChunk)

	case 16:
		batchG2AffineMsm[bucketG2AffineC16, bucketg2JacExtendedC16](p, 16, points, scalars, splitFirstChunk)

	case 17:
		batchG2AffineMsm[bucketG2AffineC17, bucketg2JacExtendedC10](p, 17, points, scalars, splitFirstChunk)

	case 18:
		batchG2AffineMsm[bucketG2AffineC18, bucketg2JacExtendedC6](p, 18, points, scalars, splitFirstChunk)

	case 19:
		batchG2AffineMsm[bucketG2AffineC19, bucketg2JacExtendedC4](p, 19, points, scalars, splitFirstChunk)

	case 20:
		batchG2AffineMsm[bucketG2AffineC20, bucketg2JacExtendedC4](p, 20, points, scalars, splitFirstChunk)

	case 21:
		batchG2AffineMsm[bucketG2AffineC21, bucketg2JacExtendedC6](p, 21, points, scalars, splitFirstChunk)

	case 22:
		batchG2AffineMsm[bucketG2AffineC22, bucketg2JacExtendedC10](p, 22, points, scalars, splitFirstChunk)

	case 23:
		batchG2AffineMsm[bucketG2AffineC23, bucketg2JacExtendedC16](p, 23, points, scalars, splitFirstChunk)

	default:
		panic("not implemented")
	}
}

type BatchG2Affine[B ibG2Affine] struct {
	P         [MAX_BATCH_SIZE]G2Affine
	R         [MAX_BATCH_SIZE]*G2Affine
	batchSize int
	cptP      int
	bucketIds map[uint32]struct{}
	points    []G2Affine
	buckets   *B
}

func newBatchG2Affine[B ibG2Affine](buckets *B, points []G2Affine) BatchG2Affine[B] {
	batchSize := len(*buckets) / 5
	if batchSize > MAX_BATCH_SIZE {
		batchSize = MAX_BATCH_SIZE
	}
	if batchSize <= 0 {
		batchSize = 1
	}
	return BatchG2Affine[B]{
		buckets:   buckets,
		points:    points,
		batchSize: batchSize,
		bucketIds: make(map[uint32]struct{}, len(*buckets)/2),
	}
}

func (b *BatchG2Affine[B]) IsFull() bool {
	return b.cptP == b.batchSize
}

func (b *BatchG2Affine[B]) ExecuteAndReset() {
	if b.cptP == 0 {
		return
	}
	// for i := 0; i < len(b.R); i++ {
	// 	b.R[i].Add(b.R[i], b.P[i])
	// }
	BatchAddG2Affine(b.R[:b.cptP], b.P[:b.cptP], b.cptP)
	for k := range b.bucketIds {
		delete(b.bucketIds, k)
	}
	// b.bucketIds = [MAX_BATCH_SIZE]uint32{}
	b.cptP = 0
}

func (b *BatchG2Affine[B]) CanAdd(bID uint32) bool {
	_, ok := b.bucketIds[bID]
	return !ok
}

func (b *BatchG2Affine[B]) Add(op batchOp) {
	// CanAdd must be called before --> ensures bucket is not "used" in current batch

	BK := &(*b.buckets)[op.bucketID]
	P := &b.points[op.pointID>>1]
	if P.IsInfinity() {
		return
	}
	// handle special cases with inf or -P / P
	if BK.IsInfinity() {
		if op.isNeg() {
			BK.Neg(P)
		} else {
			BK.Set(P)
		}
		return
	}
	if op.isNeg() {
		// if bucket == P --> -P == 0
		if BK.Equal(P) {
			BK.setInfinity()
			return
		}
	} else {
		// if bucket == -P, B == 0
		if BK.X.Equal(&P.X) && !BK.Y.Equal(&P.Y) {
			BK.setInfinity()
			return
		}
	}

	// b.bucketIds[b.cptP] = op.bucketID
	b.bucketIds[op.bucketID] = struct{}{}
	b.R[b.cptP] = BK
	if op.isNeg() {
		b.P[b.cptP].Neg(P)
	} else {
		b.P[b.cptP].Set(P)
	}
	b.cptP++
}

func processQueueG2Affine[B ibG2Affine](queue []batchOp, batch *BatchG2Affine[B]) []batchOp {
	for i := len(queue) - 1; i >= 0; i-- {
		if batch.CanAdd(queue[i].bucketID) {
			batch.Add(queue[i])
			if batch.IsFull() {
				batch.ExecuteAndReset()
			}
			queue[i] = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		}
	}
	return queue

}

func msmProcessChunkG2AffineBatchAffine[B ibG2Affine](chunk uint64,
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

	batch := newBatchG2Affine(&buckets, points)
	queue := make([]batchOp, 0, 4096) // TODO find right capacity here.
	nbBatches := 0
	for i := 0; i < len(scalars); i++ {
		bits := (scalars[i][s.index] & s.mask) >> s.shift
		if s.multiWordSelect {
			bits += (scalars[i][s.index+1] & s.maskHigh) << s.shiftHigh
		}

		if bits == 0 {
			continue
		}

		op := batchOp{pointID: uint32(i) << 1}
		// if msbWindow bit is set, we need to substract
		if bits&msbWindow == 0 {
			// add
			op.bucketID = uint32(bits - 1)
			// buckets[bits-1].Add(&points[i], &buckets[bits-1])
		} else {
			// sub
			op.bucketID = (uint32(bits & ^msbWindow))
			op.pointID += 1
			// op.isNeg = true
			// buckets[bits & ^msbWindow].Sub( &buckets[bits & ^msbWindow], &points[i])
		}
		if batch.CanAdd(op.bucketID) {
			batch.Add(op)
			if batch.IsFull() {
				batch.ExecuteAndReset()
				nbBatches++
				if len(queue) != 0 { // TODO @gbotrel this doesn't seem to help much? should minimize queue resizing
					batch.Add(queue[len(queue)-1])
					queue = queue[:len(queue)-1]
				}
			}
		} else {
			// put it in queue.
			queue = append(queue, op)
		}
	}
	// fmt.Printf("chunk %d\nlen(queue)=%d\nnbBatches=%d\nbatchSize=%d\nnbBuckets=%d\nnbPoints=%d\n",
	// 	chunk, len(queue), nbBatches, batch.batchSize, len(buckets), len(points))
	// batch.ExecuteAndReset()
	for len(queue) != 0 {
		queue = processQueueG2Affine(queue, &batch)
		batch.ExecuteAndReset() // execute batch even if not full.
	}

	// flush items in batch.
	batch.ExecuteAndReset()

	// reduce buckets into total
	// total =  bucket[0] + 2*bucket[1] + 3*bucket[2] ... + n*bucket[n-1]

	var runningSum, total g2JacExtended
	runningSum.setInfinity()
	total.setInfinity()
	for k := len(buckets) - 1; k >= 0; k-- {
		if !buckets[k].IsInfinity() {
			runningSum.addMixed(&buckets[k])
		}
		total.add(&runningSum)
	}

	chRes <- total

}

func batchG2AffineMsm[B ibG2Affine, J ibg2JacExtended](p *G2Jac, c uint64, points []G2Affine, scalars []fr.Element, splitFirstChunk bool) *G2Jac {

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
			// TODO @gbotrel lastC restore.
			msmProcessChunkG2Affine[J](j, chChunks[j], c, points, scalars)
		}(uint64(nbChunks-1), points, scalars)
		nbChunks--
	}

	processChunk := func(j int, points []G2Affine, scalars []fr.Element, chChunk chan g2JacExtended) {
		msmProcessChunkG2AffineBatchAffine[B](uint64(j), chChunk, c, points, scalars)
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

type bucketG2AffineC1 [1 << (1 - 1)]G2Affine
type bucketG2AffineC2 [1 << (2 - 1)]G2Affine
type bucketG2AffineC3 [1 << (3 - 1)]G2Affine
type bucketG2AffineC4 [1 << (4 - 1)]G2Affine
type bucketG2AffineC5 [1 << (5 - 1)]G2Affine
type bucketG2AffineC6 [1 << (6 - 1)]G2Affine
type bucketG2AffineC7 [1 << (7 - 1)]G2Affine
type bucketG2AffineC8 [1 << (8 - 1)]G2Affine
type bucketG2AffineC9 [1 << (9 - 1)]G2Affine
type bucketG2AffineC10 [1 << (10 - 1)]G2Affine
type bucketG2AffineC11 [1 << (11 - 1)]G2Affine
type bucketG2AffineC12 [1 << (12 - 1)]G2Affine
type bucketG2AffineC13 [1 << (13 - 1)]G2Affine
type bucketG2AffineC14 [1 << (14 - 1)]G2Affine
type bucketG2AffineC15 [1 << (15 - 1)]G2Affine
type bucketG2AffineC16 [1 << (16 - 1)]G2Affine
type bucketG2AffineC17 [1 << (17 - 1)]G2Affine
type bucketG2AffineC18 [1 << (18 - 1)]G2Affine
type bucketG2AffineC19 [1 << (19 - 1)]G2Affine
type bucketG2AffineC20 [1 << (20 - 1)]G2Affine
type bucketG2AffineC21 [1 << (21 - 1)]G2Affine
type bucketG2AffineC22 [1 << (22 - 1)]G2Affine
type bucketG2AffineC23 [1 << (23 - 1)]G2Affine
type bucketg2JacExtendedC1 [1 << (1 - 1)]g2JacExtended
type bucketg2JacExtendedC2 [1 << (2 - 1)]g2JacExtended
type bucketg2JacExtendedC3 [1 << (3 - 1)]g2JacExtended
type bucketg2JacExtendedC4 [1 << (4 - 1)]g2JacExtended
type bucketg2JacExtendedC5 [1 << (5 - 1)]g2JacExtended
type bucketg2JacExtendedC6 [1 << (6 - 1)]g2JacExtended
type bucketg2JacExtendedC7 [1 << (7 - 1)]g2JacExtended
type bucketg2JacExtendedC8 [1 << (8 - 1)]g2JacExtended
type bucketg2JacExtendedC9 [1 << (9 - 1)]g2JacExtended
type bucketg2JacExtendedC10 [1 << (10 - 1)]g2JacExtended
type bucketg2JacExtendedC11 [1 << (11 - 1)]g2JacExtended
type bucketg2JacExtendedC12 [1 << (12 - 1)]g2JacExtended
type bucketg2JacExtendedC13 [1 << (13 - 1)]g2JacExtended
type bucketg2JacExtendedC14 [1 << (14 - 1)]g2JacExtended
type bucketg2JacExtendedC15 [1 << (15 - 1)]g2JacExtended
type bucketg2JacExtendedC16 [1 << (16 - 1)]g2JacExtended
type bucketg2JacExtendedC17 [1 << (17 - 1)]g2JacExtended
type bucketg2JacExtendedC18 [1 << (18 - 1)]g2JacExtended
type bucketg2JacExtendedC19 [1 << (19 - 1)]g2JacExtended
type bucketg2JacExtendedC20 [1 << (20 - 1)]g2JacExtended
type bucketg2JacExtendedC21 [1 << (21 - 1)]g2JacExtended
type bucketg2JacExtendedC22 [1 << (22 - 1)]g2JacExtended
type bucketg2JacExtendedC23 [1 << (23 - 1)]g2JacExtended

type ibG2Affine interface {
	bucketG2AffineC1 |
		bucketG2AffineC2 |
		bucketG2AffineC3 |
		bucketG2AffineC4 |
		bucketG2AffineC5 |
		bucketG2AffineC6 |
		bucketG2AffineC7 |
		bucketG2AffineC8 |
		bucketG2AffineC9 |
		bucketG2AffineC10 |
		bucketG2AffineC11 |
		bucketG2AffineC12 |
		bucketG2AffineC13 |
		bucketG2AffineC14 |
		bucketG2AffineC15 |
		bucketG2AffineC16 |
		bucketG2AffineC17 |
		bucketG2AffineC18 |
		bucketG2AffineC19 |
		bucketG2AffineC20 |
		bucketG2AffineC21 |
		bucketG2AffineC22 |
		bucketG2AffineC23
}

type ibg2JacExtended interface {
	bucketg2JacExtendedC1 |
		bucketg2JacExtendedC2 |
		bucketg2JacExtendedC3 |
		bucketg2JacExtendedC4 |
		bucketg2JacExtendedC5 |
		bucketg2JacExtendedC6 |
		bucketg2JacExtendedC7 |
		bucketg2JacExtendedC8 |
		bucketg2JacExtendedC9 |
		bucketg2JacExtendedC10 |
		bucketg2JacExtendedC11 |
		bucketg2JacExtendedC12 |
		bucketg2JacExtendedC13 |
		bucketg2JacExtendedC14 |
		bucketg2JacExtendedC15 |
		bucketg2JacExtendedC16 |
		bucketg2JacExtendedC17 |
		bucketg2JacExtendedC18 |
		bucketg2JacExtendedC19 |
		bucketg2JacExtendedC20 |
		bucketg2JacExtendedC21 |
		bucketg2JacExtendedC22 |
		bucketg2JacExtendedC23
}
