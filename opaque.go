// SPDX-License-Identifier: MIT
//
// Copyright (C) 2020-2025 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package opaque implements OPAQUE, an asymmetric password-authenticated key exchange protocol that is secure against
// pre-computation attacks. It enables a client to authenticate to a server without ever revealing its password to the
// server. Protocol details can be found on the IETF RFC page (https://datatracker.ietf.org/doc/draft-irtf-cfrg-opaque)
// and on the GitHub specification repository (https://github.com/cfrg/draft-irtf-cfrg-opaque).
package opaque

import (
	"crypto"
	"errors"
	"fmt"

	"github.com/bytemare/ecc"
	"github.com/bytemare/hash"
	"github.com/bytemare/ksf"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/ake"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/oprf"
	"github.com/bytemare/opaque/message"
)

// Group identifies the prime-order group with hash-to-curve capability to use in OPRF and AKE.
type Group byte

const (
	// RistrettoSha512 identifies the Ristretto255 group and SHA-512.
	RistrettoSha512 = Group(ecc.Ristretto255Sha512)

	// decaf448Shake256 identifies the Decaf448 group and Shake-256.
	// decaf448Shake256 = 2.

	// P256Sha256 identifies the NIST P-256 group and SHA-256.
	P256Sha256 = Group(ecc.P256Sha256)

	// P384Sha512 identifies the NIST P-384 group and SHA-384.
	P384Sha512 = Group(ecc.P384Sha384)

	// P521Sha512 identifies the NIST P-512 group and SHA-512.
	P521Sha512 = Group(ecc.P521Sha512)
)

// Available returns whether the Group byte is recognized in this implementation. This allows to fail early when
// working with multiple versions not using the same configuration and ecc.
func (g Group) Available() bool {
	return g == RistrettoSha512 ||
		g == P256Sha256 ||
		g == P384Sha512 ||
		g == P521Sha512
}

// OPRF returns the OPRF Identifier used in the Ciphersuite.
func (g Group) OPRF() oprf.Identifier {
	return oprf.IDFromGroup(g.Group())
}

// Group returns the EC Group used in the Ciphersuite.
func (g Group) Group() ecc.Group {
	return ecc.Group(g)
}

const confIDsLength = 6

var (
	errInvalidOPRFid = errors.New("invalid OPRF group id")
	errInvalidKDFid  = errors.New("invalid KDF id")
	errInvalidMACid  = errors.New("invalid MAC id")
	errInvalidHASHid = errors.New("invalid Hash id")
	errInvalidKSFid  = errors.New("invalid KSF id")
	errInvalidAKEid  = errors.New("invalid AKE group id")
)

// Configuration represents an OPAQUE configuration. Note that OprfGroup and AKEGroup are recommended to be the same,
// as well as KDF, MAC, Hash should be the same.
type Configuration struct {
	Context []byte
	KDF     crypto.Hash    `json:"kdf"`
	MAC     crypto.Hash    `json:"mac"`
	Hash    crypto.Hash    `json:"hash"`
	KSF     ksf.Identifier `json:"ksf"`
	OPRF    Group          `json:"oprf"`
	AKE     Group          `json:"group"`
}

// DefaultConfiguration returns a default configuration with strong parameters.
func DefaultConfiguration() *Configuration {
	return &Configuration{
		OPRF:    RistrettoSha512,
		AKE:     RistrettoSha512,
		KSF:     ksf.Argon2id,
		KDF:     crypto.SHA512,
		MAC:     crypto.SHA512,
		Hash:    crypto.SHA512,
		Context: nil,
	}
}

// Client returns a newly instantiated Client from the Configuration.
func (c *Configuration) Client() (*Client, error) {
	return NewClient(c)
}

// Server returns a newly instantiated Server from the Configuration.
func (c *Configuration) Server() (*Server, error) {
	return NewServer(c)
}

// GenerateOPRFSeed returns a OPRF seed valid in the given configuration.
func (c *Configuration) GenerateOPRFSeed() []byte {
	return RandomBytes(c.Hash.Size())
}

// KeyGen returns a key pair in the AKE ecc.
func (c *Configuration) KeyGen() (secretKey, publicKey []byte) {
	return ake.KeyGen(ecc.Group(c.AKE))
}

// verify returns an error on the first non-compliant parameter, nil otherwise.
func (c *Configuration) verify() error {
	if !c.OPRF.Available() || !c.OPRF.OPRF().Available() {
		return errInvalidOPRFid
	}

	if !c.AKE.Available() || !c.AKE.Group().Available() {
		return errInvalidAKEid
	}

	if c.KDF >= 25 || !hash.Hash(c.KDF).Available() { //nolint:gosec // overflow is checked beforehand.
		return errInvalidKDFid
	}

	if c.MAC >= 25 || !hash.Hash(c.MAC).Available() { //nolint:gosec // overflow is checked beforehand.
		return errInvalidMACid
	}

	if c.Hash >= 25 || !hash.Hash(c.Hash).Available() { //nolint:gosec // overflow is checked beforehand.
		return errInvalidHASHid
	}

	if c.KSF != 0 && !c.KSF.Available() {
		return errInvalidKSFid
	}

	return nil
}

// toInternal builds the internal representation of the configuration parameters.
func (c *Configuration) toInternal() (*internal.Configuration, error) {
	if err := c.verify(); err != nil {
		return nil, err
	}

	g := c.AKE.Group()
	o := c.OPRF.OPRF()
	mac := internal.NewMac(c.MAC)
	ip := &internal.Configuration{
		OPRF:         o,
		Group:        g,
		KSF:          internal.NewKSF(c.KSF),
		KDF:          internal.NewKDF(c.KDF),
		MAC:          mac,
		Hash:         internal.NewHash(c.Hash),
		NonceLen:     internal.NonceLength,
		EnvelopeSize: internal.NonceLength + mac.Size(),
		Context:      c.Context,
	}

	return ip, nil
}

// Deserializer returns a pointer to a Deserializer structure allowing deserialization of messages in the given
// configuration.
func (c *Configuration) Deserializer() (*Deserializer, error) {
	conf, err := c.toInternal()
	if err != nil {
		return nil, err
	}

	return &Deserializer{conf: conf}, nil
}

// Serialize returns the byte encoding of the Configuration structure.
func (c *Configuration) Serialize() []byte {
	ids := []byte{
		byte(c.OPRF),
		byte(c.AKE),
		byte(c.KSF),
		byte(c.KDF),
		byte(c.MAC),
		byte(c.Hash),
	}

	return encoding.Concatenate(ids, encoding.EncodeVector(c.Context))
}

// DeserializeConfiguration decodes the input and returns a Parameter structure.
func DeserializeConfiguration(encoded []byte) (*Configuration, error) {
	// corresponds to the configuration length + 2-byte encoding of empty context
	if len(encoded) < confIDsLength+2 {
		return nil, internal.ErrConfigurationInvalidLength
	}

	ctx, _, err := encoding.DecodeVector(encoded[confIDsLength:])
	if err != nil {
		return nil, fmt.Errorf("decoding the configuration context: %w", err)
	}

	c := &Configuration{
		OPRF:    Group(encoded[0]),
		AKE:     Group(encoded[1]),
		KSF:     ksf.Identifier(encoded[2]),
		KDF:     crypto.Hash(encoded[3]),
		MAC:     crypto.Hash(encoded[4]),
		Hash:    crypto.Hash(encoded[5]),
		Context: ctx,
	}

	if err2 := c.verify(); err2 != nil {
		return nil, err2
	}

	return c, nil
}

// GetFakeRecord creates a fake Client record to be used when no existing client record exists,
// to defend against client enumeration techniques.
func (c *Configuration) GetFakeRecord(credentialIdentifier []byte) (*ClientRecord, error) {
	i, err := c.toInternal()
	if err != nil {
		return nil, err
	}

	scalar := i.Group.NewScalar().Random()
	publicKey := i.Group.Base().Multiply(scalar)

	regRecord := &message.RegistrationRecord{
		PublicKey:  publicKey,
		MaskingKey: RandomBytes(i.KDF.Size()),
		Envelope:   make([]byte, internal.NonceLength+i.MAC.Size()),
	}

	return &ClientRecord{
		CredentialIdentifier: credentialIdentifier,
		ClientIdentity:       nil,
		RegistrationRecord:   regRecord,
	}, nil
}

// ClientRecord is a server-side structure enabling the storage of user relevant information.
type ClientRecord struct {
	*message.RegistrationRecord
	CredentialIdentifier []byte
	ClientIdentity       []byte
}

// RandomBytes returns random bytes of length len (wrapper for crypto/rand).
func RandomBytes(length int) []byte {
	return internal.RandomBytes(length)
}
