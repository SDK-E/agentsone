// SPDX-License-Identifier: MIT

package gatewaytoken

// SignUnchecked signs claims without validating them, so tests can build
// tokens that carry a good MAC over bad claims.
func SignUnchecked(key []byte, c Claims) string { return sign(key, c) }

// MAC is the token MAC over signed, so tests can sign raw claim bytes.
func MAC(key []byte, signed string) []byte { return mac(key, signed) }
