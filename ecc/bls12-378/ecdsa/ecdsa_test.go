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

package ecdsa

import (
	"crypto/rand"
	"crypto/sha512"
	"testing"

	"github.com/leanovate/gopter"
	"github.com/leanovate/gopter/prop"
)

func TestECDSA(t *testing.T) {

	t.Parallel()
	parameters := gopter.DefaultTestParameters()
	properties := gopter.NewProperties(parameters)

	properties.Property("[BLS12-378] test the signing and verification", prop.ForAll(
		func() bool {

			privKey, _ := GenerateKey(rand.Reader)
			publicKey := privKey.PublicKey

			msg := []byte("testing ECDSA")
			sig, _ := privKey.Sign(msg, rand.Reader)

			md := sha512.New()
			flag, _ := publicKey.Verify(sig, msg, md)

			return flag
		},
	))

	properties.TestingRun(t, gopter.ConsoleReporter(false))
}

// ------------------------------------------------------------
// benches

func BenchmarkSignECDSA(b *testing.B) {

	privKey, _ := GenerateKey(rand.Reader)

	msg := []byte("benchmarking ECDSA sign()")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		privKey.Sign(msg, rand.Reader)
	}
}

func BenchmarkVerifyECDSA(b *testing.B) {

	privKey, _ := GenerateKey(rand.Reader)
	msg := []byte("benchmarking ECDSA sign()")
	sig, _ := privKey.Sign(msg, rand.Reader)
	md := sha512.New()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		privKey.PublicKey.Verify(sig, msg, md)
	}
}
