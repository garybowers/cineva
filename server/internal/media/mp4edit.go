package media

import "encoding/binary"

// stripEdts removes every edts box from an MP4 init segment, rewriting
// ancestor box sizes. Returns the input unchanged if it doesn't parse.
func stripEdts(data []byte) []byte {
	out, ok := stripEdtsWalk(data)
	if !ok {
		return data
	}
	return out
}

// containers whose children we descend into looking for edts.
var edtsAncestors = map[string]bool{"moov": true, "trak": true}

func stripEdtsWalk(buf []byte) ([]byte, bool) {
	out := make([]byte, 0, len(buf))
	off := 0
	for off+8 <= len(buf) {
		size := int(binary.BigEndian.Uint32(buf[off : off+4]))
		typ := string(buf[off+4 : off+8])
		if size < 8 || off+size > len(buf) {
			return nil, false
		}
		switch {
		case typ == "edts":
			// drop it
		case edtsAncestors[typ]:
			body, ok := stripEdtsWalk(buf[off+8 : off+size])
			if !ok {
				return nil, false
			}
			var hdr [8]byte
			binary.BigEndian.PutUint32(hdr[:4], uint32(len(body)+8))
			copy(hdr[4:], typ)
			out = append(out, hdr[:]...)
			out = append(out, body...)
		default:
			out = append(out, buf[off:off+size]...)
		}
		off += size
	}
	if off != len(buf) {
		return nil, false
	}
	return out, true
}
