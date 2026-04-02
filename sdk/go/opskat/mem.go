//go:build wasip1

package opskat

import "unsafe"

// pinned keeps references to malloc'd buffers so the Go GC does not reclaim
// them before the host has finished writing and the guest has read the data.
// Each WASM module instance is short-lived (one request), so this is fine.
var pinned [][]byte

// malloc is exported for the host to allocate guest memory when returning data.
//
//go:wasmexport malloc
func malloc(size uint32) uint32 {
	buf := make([]byte, size)
	if len(buf) == 0 {
		return 0
	}
	pinned = append(pinned, buf)
	return uint32(uintptr(unsafe.Pointer(&buf[0])))
}

// free is exported for the host to release guest memory.
// Each WASM instance is short-lived, so this is a no-op.
//
//go:wasmexport free
func free(ptr uint32) {}
