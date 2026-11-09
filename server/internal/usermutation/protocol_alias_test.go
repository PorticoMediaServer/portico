package usermutation

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/mutationreceipt"
)

func TestProtocolSentinelsAndDTOAliases(t *testing.T) {
	for _, pair := range [][2]error{{ErrRootGone, mutationreceipt.ErrRootGone}, {ErrInvalidKey, mutationreceipt.ErrInvalidKey}, {ErrExpired, mutationreceipt.ErrExpired}, {ErrConflict, mutationreceipt.ErrConflict}, {ErrCapacity, mutationreceipt.ErrCapacity}} {
		if pair[0] != pair[1] || !errors.Is(pair[0], pair[1]) {
			t.Fatal("protocol sentinel identity changed")
		}
	}
	var native mutationreceipt.Receipt
	var legacy Receipt = native
	native = legacy
	if native != legacy {
		t.Fatal("receipt DTO alias changed")
	}
}
