// SPDX-License-Identifier: MIT
//
// Copyright (C) 2020-2025 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

package opaque

import (
	"errors"
	"fmt"

	"github.com/bytemare/ecc"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/ake"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/masking"
	"github.com/bytemare/opaque/internal/tag"
	"github.com/bytemare/opaque/message"
)

var (
	// ErrNoServerKeyMaterial indicates that the server's key material has not been set.
	ErrNoServerKeyMaterial = errors.New("key material not set: call SetKeyMaterial() to set values")

	// ErrAkeInvalidClientMac indicates that the MAC contained in the KE3 message is not valid in the given session.
	ErrAkeInvalidClientMac = errors.New("failed to authenticate client: invalid client mac")

	// ErrInvalidState indicates that the given state is not valid due to a wrong length.
	ErrInvalidState = errors.New("invalid state length")

	// ErrInvalidEnvelopeLength indicates the envelope contained in the record is of invalid length.
	ErrInvalidEnvelopeLength = errors.New("record has invalid envelope length")

	// ErrInvalidPksLength indicates the input public key is not of right length.
	ErrInvalidPksLength = errors.New("input server public key's length is invalid")

	// ErrInvalidOPRFSeedLength indicates that the OPRF seed is not of right length.
	ErrInvalidOPRFSeedLength = errors.New("input OPRF seed length is invalid (must be of hash output length)")

	// ErrZeroSKS indicates that the server's private key is a zero scalar.
	ErrZeroSKS = errors.New("server private key is zero")
)

// Server represents an OPAQUE Server, exposing its functions and holding its state.
type Server struct {
	Deserialize *Deserializer
	conf        *internal.Configuration
	Ake         *ake.Server
	*keyMaterial
}

type keyMaterial struct {
	serverIdentity  []byte
	serverSecretKey *ecc.Scalar
	serverPublicKey []byte
	oprfSeed        []byte
}

// NewServer returns a Server instantiation given the application Configuration.
func NewServer(c *Configuration) (*Server, error) {
	if c == nil {
		c = DefaultConfiguration()
	}

	conf, err := c.toInternal()
	if err != nil {
		return nil, err
	}

	return &Server{
		Deserialize: &Deserializer{conf: conf},
		conf:        conf,
		Ake:         ake.NewServer(),
		keyMaterial: nil,
	}, nil
}

// GetConf return the internal configuration.
func (s *Server) GetConf() *internal.Configuration {
	return s.conf
}

func (s *Server) oprfResponse(element *ecc.Element, oprfSeed, credentialIdentifier []byte) *ecc.Element {
	seed := s.conf.KDF.Expand(
		oprfSeed,
		encoding.SuffixString(credentialIdentifier, tag.ExpandOPRF),
		internal.SeedLength,
	)
	ku := s.conf.OPRF.DeriveKey(seed, []byte(tag.DeriveKeyPair))

	return s.conf.OPRF.Evaluate(ku, element)
}

// RegistrationResponse returns a RegistrationResponse message to the input RegistrationRequest message and given
// identifiers.
func (s *Server) RegistrationResponse(
	req *message.RegistrationRequest,
	serverPublicKey *ecc.Element,
	credentialIdentifier, oprfSeed []byte,
) *message.RegistrationResponse {
	z := s.oprfResponse(req.BlindedMessage, oprfSeed, credentialIdentifier)

	return &message.RegistrationResponse{
		EvaluatedMessage: z,
		Pks:              serverPublicKey,
	}
}

func (s *Server) credentialResponse(
	req *message.CredentialRequest,
	serverPublicKey []byte,
	record *message.RegistrationRecord,
	credentialIdentifier, oprfSeed, maskingNonce []byte,
) *message.CredentialResponse {
	z := s.oprfResponse(req.BlindedMessage, oprfSeed, credentialIdentifier)

	maskingNonce, maskedResponse := masking.Mask(
		s.conf,
		maskingNonce,
		record.MaskingKey,
		serverPublicKey,
		record.Envelope,
	)

	return message.NewCredentialResponse(z, maskingNonce, maskedResponse)
}

// GenerateKE2Options enable setting optional values for the session, which default to secure random values if not
// set.
type GenerateKE2Options struct {
	// KeyShareSeed: optional.
	KeyShareSeed []byte
	// AKENonce: optional.
	AKENonce []byte
	// MaskingNonce: optional.
	MaskingNonce []byte
	// AKENonceLength: optional, overrides the default length of the nonce to be created if no nonce is provided.
	AKENonceLength uint32
}

func getGenerateKE2Options(options []GenerateKE2Options) (*ake.Options, []byte) {
	var (
		op           ake.Options
		maskingNonce []byte
	)

	if len(options) != 0 {
		op.KeyShareSeed = options[0].KeyShareSeed
		op.Nonce = options[0].AKENonce
		op.NonceLength = options[0].AKENonceLength
		maskingNonce = options[0].MaskingNonce
	}

	return &op, maskingNonce
}

// SetKeyMaterial set the server's identity and mandatory key material to be used during GenerateKE2().
// All these values must be the same as used during client registration and remain the same across protocol execution
// for a given registered client.
//
// - serverIdentity can be nil, in which case it will be set to serverPublicKey.
// - serverSecretKey is the server's secret AKE key.
// - serverPublicKey is the server's public AKE key to the serverSecretKey.
// - oprfSeed is the long-term OPRF input seed.
func (s *Server) SetKeyMaterial(serverIdentity, serverSecretKey, serverPublicKey, oprfSeed []byte) error {
	sks := s.conf.Group.NewScalar()
	if err := sks.Decode(serverSecretKey); err != nil {
		return fmt.Errorf("invalid server AKE secret key: %w", err)
	}

	if sks.IsZero() {
		return ErrZeroSKS
	}

	if len(oprfSeed) != s.conf.Hash.Size() {
		return ErrInvalidOPRFSeedLength
	}

	if len(serverPublicKey) != s.conf.Group.ElementLength() {
		return ErrInvalidPksLength
	}

	if err := s.conf.Group.NewElement().Decode(serverPublicKey); err != nil {
		return fmt.Errorf("invalid server public key: %w", err)
	}

	s.keyMaterial = &keyMaterial{
		serverIdentity:  serverIdentity,
		serverSecretKey: sks,
		serverPublicKey: serverPublicKey,
		oprfSeed:        oprfSeed,
	}

	return nil
}

// GenerateKE2 responds to a KE1 message with a KE2 message a client record.
func (s *Server) GenerateKE2(
	ke1 *message.KE1,
	record *ClientRecord,
	options ...GenerateKE2Options,
) (*message.KE2, error) {
	if s.keyMaterial == nil {
		return nil, ErrNoServerKeyMaterial
	}

	if len(record.Envelope) != s.conf.EnvelopeSize {
		return nil, ErrInvalidEnvelopeLength
	}

	// We've checked that the server's public key and the client's envelope are of correct length,
	// thus ensuring that the subsequent xor-ing input is the same length as the encryption pad.

	op, maskingNonce := getGenerateKE2Options(options)

	response := s.credentialResponse(ke1.CredentialRequest, s.serverPublicKey,
		record.RegistrationRecord, record.CredentialIdentifier, s.oprfSeed, maskingNonce)

	identities := ake.Identities{
		ClientIdentity: record.ClientIdentity,
		ServerIdentity: s.serverIdentity,
	}
	identities.SetIdentities(record.PublicKey, s.serverPublicKey)

	ke2 := s.Ake.Response(s.conf, &identities, s.serverSecretKey, record.PublicKey, ke1, response, *op)

	return ke2, nil
}

// LoginFinish returns an error if the KE3 received from the client holds an invalid mac, and nil if correct.
func (s *Server) LoginFinish(ke3 *message.KE3) error {
	if !s.Ake.Finalize(s.conf, ke3) {
		return ErrAkeInvalidClientMac
	}

	return nil
}

// SessionKey returns the session key if the previous call to GenerateKE2() was successful.
func (s *Server) SessionKey() []byte {
	return s.Ake.SessionKey()
}

// ExpectedMAC returns the expected client MAC if the previous call to GenerateKE2() was successful.
func (s *Server) ExpectedMAC() []byte {
	return s.Ake.ExpectedMAC()
}

// SetAKEState sets the internal state of the AKE server from the given bytes.
func (s *Server) SetAKEState(state []byte) error {
	if len(state) != s.conf.MAC.Size()+s.conf.KDF.Size() {
		return ErrInvalidState
	}

	if err := s.Ake.SetState(state[:s.conf.MAC.Size()], state[s.conf.MAC.Size():]); err != nil {
		return fmt.Errorf("setting AKE state: %w", err)
	}

	return nil
}

// SerializeState returns the internal state of the AKE server serialized to bytes.
func (s *Server) SerializeState() []byte {
	return s.Ake.SerializeState()
}
