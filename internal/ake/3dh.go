// SPDX-License-Identifier: MIT
//
// Copyright (C) 2021 Daniel Bourdrez. All Rights Reserved.
//
// This source code is licensed under the MIT license found in the
// LICENSE file in the root directory of this source tree or at
// https://spdx.org/licenses/MIT.html

// Package ake provides high-level functions for the 3DH AKE.
package ake

import (
	"github.com/bytemare/crypto/group"

	"github.com/bytemare/opaque/internal"
	"github.com/bytemare/opaque/internal/encoding"
	"github.com/bytemare/opaque/internal/tag"
	"github.com/bytemare/opaque/message"
)

// KeyGen returns private and public keys in the group.
func KeyGen(id group.Group) (sk, pk []byte) {
	scalar := id.NewScalar().Random()
	publicKey := id.Base().Mult(scalar)

	return encoding.SerializeScalar(scalar, id), encoding.SerializePoint(publicKey, id)
}

// setValues - testing: integrated to support testing, to force values.
// There's no effect if esk, epk, and nonce have already been set in a previous call.
func setValues(g group.Group, scalar *group.Scalar, nonce []byte, nonceLen int) (s *group.Scalar, n []byte) {
	if scalar != nil {
		s = scalar
	} else {
		s = g.NewScalar().Random()
	}

	if len(nonce) == 0 {
		nonce = internal.RandomBytes(nonceLen)
	}

	return s, nonce
}

func buildLabel(length int, label, context []byte) []byte {
	return encoding.Concat3(
		encoding.I2OSP(length, 2),
		encoding.EncodeVectorLen(append([]byte(tag.LabelPrefix), label...), 1),
		encoding.EncodeVectorLen(context, 1))
}

func expand(h *internal.KDF, secret, hkdfLabel []byte) []byte {
	return h.Expand(secret, hkdfLabel, h.Size())
}

func expandLabel(h *internal.KDF, secret, label, context []byte) []byte {
	hkdfLabel := buildLabel(h.Size(), label, context)
	return expand(h, secret, hkdfLabel)
}

func deriveSecret(h *internal.KDF, secret, label, context []byte) []byte {
	return expandLabel(h, secret, label, context)
}

func initTranscript(p *internal.Parameters, idc, ids []byte, ke1 *message.KE1, ke2 *message.KE2) {
	sidc := encoding.EncodeVector(idc)
	sids := encoding.EncodeVector(ids)
	p.Hash.Write(encoding.Concatenate([]byte(tag.VersionTag), encoding.EncodeVector(p.Context),
		sidc, ke1.Serialize(),
		sids, ke2.CredentialResponse.Serialize(), ke2.NonceS, encoding.SerializePoint(ke2.EpkS, p.Group)))
}

type macKeys struct {
	serverMacKey, clientMacKey []byte
}

func deriveKeys(h *internal.KDF, ikm, context []byte) (k *macKeys, sessionSecret []byte) {
	prk := h.Extract(nil, ikm)
	k = &macKeys{}
	handshakeSecret := deriveSecret(h, prk, []byte(tag.Handshake), context)
	sessionSecret = deriveSecret(h, prk, []byte(tag.SessionKey), context)
	k.serverMacKey = expandLabel(h, handshakeSecret, []byte(tag.MacServer), nil)
	k.clientMacKey = expandLabel(h, handshakeSecret, []byte(tag.MacClient), nil)

	return k, sessionSecret
}

func k3dh(g group.Group, p1 *group.Point, s1 *group.Scalar, p2 *group.Point, s2 *group.Scalar, p3 *group.Point, s3 *group.Scalar) []byte {
	e1 := encoding.SerializePoint(p1.Mult(s1), g)
	e2 := encoding.SerializePoint(p2.Mult(s2), g)
	e3 := encoding.SerializePoint(p3.Mult(s3), g)

	return encoding.Concat3(e1, e2, e3)
}

func core3DH(p *internal.Parameters, ikm, idu, ids []byte,
	ke1 *message.KE1, ke2 *message.KE2) (sessionSecret, macS, macC []byte) {
	initTranscript(p, idu, ids, ke1, ke2)

	keys, sessionSecret := deriveKeys(p.KDF, ikm, p.Hash.Sum()) // preamble
	serverMac := p.MAC.MAC(keys.serverMacKey, p.Hash.Sum())     // transcript2
	p.Hash.Write(serverMac)
	transcript3 := p.Hash.Sum()
	clientMac := p.MAC.MAC(keys.clientMacKey, transcript3)

	return sessionSecret, serverMac, clientMac
}
