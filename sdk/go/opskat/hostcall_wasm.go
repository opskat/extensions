//go:build wasip1

package opskat

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

// currentHost is the real WASM host caller.
var currentHost hostCaller = &wasmHostCaller{}

type wasmHostCaller struct{}

//go:wasmimport opskat host_log
func wasmHostLog(levelPtr, levelLen, msgPtr, msgLen uint32)

//go:wasmimport opskat host_io_open
func wasmHostIOOpen(paramsPtr, paramsLen uint32) uint64

//go:wasmimport opskat host_io_read
func wasmHostIORead(handleID, size uint32) uint64

//go:wasmimport opskat host_io_write
func wasmHostIOWrite(handleID, dataPtr, dataLen uint32) uint32

//go:wasmimport opskat host_io_flush
func wasmHostIOFlush(handleID uint32) uint64

//go:wasmimport opskat host_io_close
func wasmHostIOClose(handleID uint32)

//go:wasmimport opskat host_asset_get_config
func wasmHostAssetGetConfig(assetID uint64) uint64

//go:wasmimport opskat host_file_dialog
func wasmHostFileDialog(paramsPtr, paramsLen uint32) uint64

//go:wasmimport opskat host_kv_get
func wasmHostKVGet(keyPtr, keyLen uint32) uint64

//go:wasmimport opskat host_kv_set
func wasmHostKVSet(keyPtr, keyLen, valPtr, valLen uint32)

//go:wasmimport opskat host_action_event
func wasmHostActionEvent(typePtr, typeLen, dataPtr, dataLen uint32)

// Helper: convert Go string to (ptr, len) for WASM.
func strToPtr(s string) (uint32, uint32) {
	if len(s) == 0 {
		return 0, 0
	}
	b := []byte(s)
	return uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b))
}

// Helper: convert Go byte slice to (ptr, len) for WASM.
func bytesToPtr(b []byte) (uint32, uint32) {
	if len(b) == 0 {
		return 0, 0
	}
	return uint32(uintptr(unsafe.Pointer(&b[0]))), uint32(len(b))
}

// Helper: unpack uint64 result from host into []byte.
// High 32 bits = ptr, low 32 bits = size.
func unpackResult(packed uint64) ([]byte, error) {
	if packed == 0 {
		return nil, nil
	}
	ptr := uint32(packed >> 32)
	size := uint32(packed & 0xFFFFFFFF)
	if size == 0 {
		return nil, nil
	}
	data := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(ptr))), size)
	// Check for error response
	if len(data) > 0 && data[0] == '{' {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &errResp) == nil && errResp.Error != "" {
			return nil, fmt.Errorf("%s", errResp.Error)
		}
	}
	return data, nil
}

func (w *wasmHostCaller) Log(level, msg string) {
	lp, ll := strToPtr(level)
	mp, ml := strToPtr(msg)
	wasmHostLog(lp, ll, mp, ml)
}

func (w *wasmHostCaller) IOOpen(params []byte) (uint32, []byte, error) {
	pp, pl := bytesToPtr(params)
	packed := wasmHostIOOpen(pp, pl)
	data, err := unpackResult(packed)
	if err != nil {
		return 0, nil, err
	}
	var resp struct {
		HandleID uint32          `json:"handle_id"`
		Meta     json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, nil, fmt.Errorf("parse io_open result: %w", err)
	}
	metaJSON, _ := json.Marshal(resp.Meta)
	return resp.HandleID, metaJSON, nil
}

func (w *wasmHostCaller) IORead(handleID uint32, size int) ([]byte, error) {
	packed := wasmHostIORead(handleID, uint32(size))
	return unpackResult(packed)
}

func (w *wasmHostCaller) IOWrite(handleID uint32, data []byte) (int, error) {
	dp, dl := bytesToPtr(data)
	n := wasmHostIOWrite(handleID, dp, dl)
	return int(n), nil
}

func (w *wasmHostCaller) IOFlush(handleID uint32) ([]byte, error) {
	packed := wasmHostIOFlush(handleID)
	return unpackResult(packed)
}

func (w *wasmHostCaller) IOClose(handleID uint32) error {
	wasmHostIOClose(handleID)
	return nil
}

func (w *wasmHostCaller) AssetGetConfig(assetID int64) (json.RawMessage, error) {
	packed := wasmHostAssetGetConfig(uint64(assetID))
	return unpackResult(packed)
}

func (w *wasmHostCaller) FileDialog(params []byte) (string, error) {
	pp, pl := bytesToPtr(params)
	packed := wasmHostFileDialog(pp, pl)
	data, err := unpackResult(packed)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (w *wasmHostCaller) KVGet(key string) ([]byte, error) {
	kp, kl := strToPtr(key)
	packed := wasmHostKVGet(kp, kl)
	return unpackResult(packed)
}

func (w *wasmHostCaller) KVSet(key string, value []byte) error {
	kp, kl := strToPtr(key)
	vp, vl := bytesToPtr(value)
	wasmHostKVSet(kp, kl, vp, vl)
	return nil
}

func (w *wasmHostCaller) ActionEvent(eventType string, data []byte) {
	tp, tl := strToPtr(eventType)
	dp, dl := bytesToPtr(data)
	wasmHostActionEvent(tp, tl, dp, dl)
}
