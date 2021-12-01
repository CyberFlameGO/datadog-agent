// This package contains the implementation of `go version`,
// extracted out and modified into a library that can be called directly
// (additionally, all binary types other than ELF have been removed).
//
// See https://cs.opensource.google/go/go/+/refs/tags/go1.17.2:src/cmd/go/internal/version/version.go
// for the original source.
//
// The original license is included in ./LICENSE

package binversion

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
)

// Exe is an interface that exposes the required operations on a binary
// needed to find and extract the Go version left by the linker.
type Exe interface {
	ReadData(addr, size uint64) ([]byte, error)
	DataStart() uint64
}

// GetElfVersion finds and returns the Go version in the given ELF binary.
func GetElfVersion(elfFile *elf.File) (string, error) {
	return GetVersion(&ElfExe{f: elfFile})
}

// The build info blob left by the linker is identified by
// a 16-byte header, consisting of buildInfoMagic (14 bytes),
// the binary's pointer size (1 byte),
// and whether the binary is big endian (1 byte).
var buildInfoMagic = []byte("\xff Go buildinf:")

// GetVersion finds and returns the Go version in the executable x.
func GetVersion(x Exe) (string, error) {
	// Read the first 64kB of text to find the build info blob.
	text := x.DataStart()
	data, err := x.ReadData(text, 64*1024)
	if err != nil {
		return "", err
	}
	for ; !bytes.HasPrefix(data, buildInfoMagic); data = data[32:] {
		if len(data) < 32 {
			return "", fmt.Errorf("reached EOF when searching for build info")
		}
	}

	// Decode the blob.
	ptrSize := int(data[14])
	bigEndian := data[15] != 0
	var bo binary.ByteOrder
	if bigEndian {
		bo = binary.BigEndian
	} else {
		bo = binary.LittleEndian
	}
	var readPtr func([]byte) uint64
	if ptrSize == 4 {
		readPtr = func(b []byte) uint64 { return uint64(bo.Uint32(b)) }
	} else {
		readPtr = bo.Uint64
	}
	vers := readString(x, ptrSize, readPtr, readPtr(data[16:]))
	if vers == "" {
		return "", fmt.Errorf("no version string found in binary")
	}

	return vers, nil
}

// readString returns the string at address addr in the executable x.
func readString(x Exe, ptrSize int, readPtr func([]byte) uint64, addr uint64) string {
	hdr, err := x.ReadData(addr, uint64(2*ptrSize))
	if err != nil || len(hdr) < 2*ptrSize {
		return ""
	}
	dataAddr := readPtr(hdr)
	dataLen := readPtr(hdr[ptrSize:])
	data, err := x.ReadData(dataAddr, dataLen)
	if err != nil || uint64(len(data)) < dataLen {
		return ""
	}
	return string(data)
}
