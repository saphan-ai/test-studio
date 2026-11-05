package main

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	// derivedFingerprintPrefix is the spec's scheme tag on a fingerprint this
	// command computed because the scanner wrote none.
	derivedFingerprintPrefix = "saphan/v1:"
	// derivedFingerprintHexLen is the number of hex characters taken from the
	// SHA-256. The spec fixes the length.
	derivedFingerprintHexLen = 16
	hexCharsPerByte          = 2
	// fingerprintSeparator is the NUL the spec places between the hashed fields.
	fingerprintSeparator byte = 0
)

// derivedFingerprint is saphan/v1: plus the first 16 hex characters of SHA-256
// over ruleId, NUL, the first location uri, NUL, and message.text. The line
// number is not an input, so an edit above the finding does not change it.
func derivedFingerprint(ruleID, uri, message string) (string, error) {
	h := sha256.New()
	parts := [][]byte{
		[]byte(ruleID),
		{fingerprintSeparator},
		[]byte(uri),
		{fingerprintSeparator},
		[]byte(message),
	}
	for _, p := range parts {
		if _, err := h.Write(p); err != nil {
			return "", err
		}
	}
	sum := h.Sum(nil)
	n := derivedFingerprintHexLen / hexCharsPerByte
	return derivedFingerprintPrefix + hex.EncodeToString(sum[:n]), nil
}
