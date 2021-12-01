package binversion

import (
	"debug/elf"
	"fmt"
)

// ElfExe is the ELF implementation of the Exe interface.
type ElfExe struct {
	f *elf.File
}

// ReadData implements Exe.ReadData for *ElfExe
func (x *ElfExe) ReadData(addr, size uint64) ([]byte, error) {
	for _, prog := range x.f.Progs {
		if prog.Vaddr <= addr && addr <= prog.Vaddr+prog.Filesz-1 {
			n := prog.Vaddr + prog.Filesz - addr
			if n > size {
				n = size
			}
			data := make([]byte, n)
			_, err := prog.ReadAt(data, int64(addr-prog.Vaddr))
			if err != nil {
				return nil, err
			}
			return data, nil
		}
	}
	return nil, fmt.Errorf("address not mapped")
}

// DataStart implements Exe.DataStart for *ElfExe
func (x *ElfExe) DataStart() uint64 {
	for _, s := range x.f.Sections {
		if s.Name == ".go.buildinfo" {
			return s.Addr
		}
	}
	for _, p := range x.f.Progs {
		if p.Type == elf.PT_LOAD && p.Flags&(elf.PF_X|elf.PF_W) == elf.PF_W {
			return p.Vaddr
		}
	}
	return 0
}
