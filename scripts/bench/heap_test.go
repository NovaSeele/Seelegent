//go:build bench

package bench

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// Retained heap (load): the daemon's in-use heap right after a forced GC (/debug/pprof/heap?gc=1), at the end of the
// warmup and again retainedSettle after the load stopped. What stays is what the load left behind.
const (
	heapStartFile = "heap-gc-start.pb.gz"
	heapEndFile   = "heap-gc-end.pb.gz"
	heapGCPath    = "/debug/pprof/heap?gc=1"
)

// inuseSpaceFile reads the inuse_space total of a heap profile file.
func inuseSpaceFile(path string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return inuseSpace(data)
}

// inuseSpace returns the sum of the inuse_space values of a heap profile (pprof's profile.proto, gzipped or not).
// It decodes only what it needs: the sample types (field 1), the samples' values (field 2) and the string table
// (field 6), so the bench needs no protobuf or pprof module.
func inuseSpace(data []byte) (int64, error) {
	if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return 0, err
		}
		if data, err = io.ReadAll(zr); err != nil {
			return 0, err
		}
	}
	var (
		types   []int64 // string index of each sample type's name
		values  [][]int64
		strings []string
	)
	err := protoFields(data, func(field, wire int, _ uint64, b []byte) error {
		switch {
		case field == 1 && wire == 2:
			var name int64
			if err := protoFields(b, func(f, w int, v uint64, _ []byte) error {
				if f == 1 && w == 0 {
					name = int64(v)
				}
				return nil
			}); err != nil {
				return err
			}
			types = append(types, name)
		case field == 2 && wire == 2:
			var vals []int64
			if err := protoFields(b, func(f, w int, v uint64, packed []byte) error {
				switch {
				case f == 2 && w == 0:
					vals = append(vals, int64(v))
				case f == 2 && w == 2:
					for len(packed) > 0 {
						x, n := binary.Uvarint(packed)
						if n <= 0 {
							return errors.New("bad packed value")
						}
						vals, packed = append(vals, int64(x)), packed[n:]
					}
				}
				return nil
			}); err != nil {
				return err
			}
			values = append(values, vals)
		case field == 6 && wire == 2:
			strings = append(strings, string(b))
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("heap profile: %w", err)
	}
	idx := -1
	for i, s := range types {
		if s >= 0 && int(s) < len(strings) && strings[s] == "inuse_space" {
			idx = i
		}
	}
	if idx < 0 {
		return 0, errors.New("heap profile: no inuse_space sample type")
	}
	var total int64
	for _, vals := range values {
		if idx < len(vals) {
			total += vals[idx]
		}
	}
	return total, nil
}

// protoFields calls fn for each field of a protobuf message: its number, wire type, and the varint value (wire 0)
// or the bytes (wire 2). Fixed-size fields are skipped.
func protoFields(data []byte, fn func(field, wire int, v uint64, b []byte) error) error {
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 {
			return errors.New("bad field key")
		}
		data = data[n:]
		field, wire := int(key>>3), int(key&7)
		var (
			v uint64
			b []byte
		)
		switch wire {
		case 0:
			if v, n = binary.Uvarint(data); n <= 0 {
				return errors.New("bad varint")
			}
			data = data[n:]
		case 1:
			if len(data) < 8 {
				return io.ErrUnexpectedEOF
			}
			data = data[8:]
		case 2:
			size, n := binary.Uvarint(data)
			if n <= 0 || size > uint64(len(data)-n) {
				return io.ErrUnexpectedEOF
			}
			b, data = data[n:n+int(size)], data[n+int(size):]
		case 5:
			if len(data) < 4 {
				return io.ErrUnexpectedEOF
			}
			data = data[4:]
		default:
			return fmt.Errorf("wire type %d", wire)
		}
		if err := fn(field, wire, v, b); err != nil {
			return err
		}
	}
	return nil
}
