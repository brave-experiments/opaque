// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package oprf implements the Elliptic Curve Oblivious Pseudorandom Function (EC-OPRF) from https://tools.ietf.org/html/draft-irtf-cfrg-voprf.
package oprf

import (
	"github.com/bytemare/cryptotools/group"
	"github.com/bytemare/cryptotools/group/ciphersuite"
	"github.com/bytemare/cryptotools/hash"

	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/tag"
)

// mode distinguishes between the OPRF base mode and the Verifiable mode.
type mode byte

// base identifies the OPRF non-verifiable, base mode.
const base mode = iota

// Ciphersuite identifies the OPRF compatible cipher suite to be used.
type Ciphersuite ciphersuite.Identifier

const (
	// RistrettoSha512 is the OPRF cipher suite of the Ristretto255 group and SHA-512.
	RistrettoSha512 Ciphersuite = iota + 1

	// P256Sha256 is the OPRF cipher suite of the NIST P-256 group and SHA-256.
	P256Sha256 Ciphersuite = iota + 2

	// P384Sha512 is the OPRF cipher suite of the NIST P-384 group and SHA-512.
	P384Sha512

	// P521Sha512 is the OPRF cipher suite of the NIST P-512 group and SHA-512.
	P521Sha512
)

var suiteToHash = make(map[Ciphersuite]hash.Hashing)

func (c Ciphersuite) register(h hash.Hashing) {
	suiteToHash[c] = h
}

// Group returns the casted identifier for the cipher suite.
func (c Ciphersuite) Group() ciphersuite.Identifier {
	return ciphersuite.Identifier(c)
}

func (c Ciphersuite) hash() hash.Hashing {
	return suiteToHash[c]
}

func contextString(id Ciphersuite) []byte {
	v := []byte(tag.OPRF)
	ctx := make([]byte, 0, len(v)+1+2)
	ctx = append(ctx, v...)
	ctx = append(ctx, encoding.I2OSP(int(base), 1)...)
	ctx = append(ctx, encoding.I2OSP(int(id), 2)...)

	return ctx
}

type oprf struct {
	group         group.Group
	hash          *hash.Hash
	contextString []byte
}

func (o *oprf) dst(prefix string) []byte {
	p := []byte(prefix)
	dst := make([]byte, 0, len(p)+len(o.contextString))
	dst = append(dst, p...)
	dst = append(dst, o.contextString...)

	return dst
}

// DeriveKey returns a scalar mapped from the input.
func (c Ciphersuite) DeriveKey(input, dst []byte) group.Scalar {
	return c.Group().HashToScalar(input, dst)
}

// Client returns an OPRF client.
func (c Ciphersuite) Client() *Client {
	client := &Client{
		oprf: &oprf{
			group:         c.Group(),
			hash:          c.hash().Get(),
			contextString: contextString(c),
		},
	}

	return client
}

func init() {
	RistrettoSha512.register(hash.SHA512)
	P256Sha256.register(hash.SHA256)
	P384Sha512.register(hash.SHA512)
	P521Sha512.register(hash.SHA512)
}