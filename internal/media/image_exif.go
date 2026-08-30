package media

import "encoding/binary"

// jpegExifOrientation reads the TIFF Orientation tag from a JPEG APP1
// segment. Malformed or absent EXIF metadata is deliberately treated as the
// normal orientation so a bad optional metadata block cannot reject a valid
// image or change its pixels.
func jpegExifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	for offset := 2; offset+1 < len(data); {
		if data[offset] != 0xff {
			offset++
			continue
		}
		for offset < len(data) && data[offset] == 0xff {
			offset++
		}
		if offset >= len(data) {
			break
		}
		marker := data[offset]
		offset++
		if marker == 0xda || marker == 0xd9 {
			break
		}
		// Standalone markers do not carry a segment length.
		if marker >= 0xd0 && marker <= 0xd7 || marker == 0x01 {
			continue
		}
		if offset+2 > len(data) {
			break
		}
		segmentLength := int(binary.BigEndian.Uint16(data[offset : offset+2]))
		if segmentLength < 2 || segmentLength-2 > len(data)-offset-2 {
			break
		}
		segment := data[offset+2 : offset+segmentLength]
		if marker == 0xe1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation := parseTIFFOrientation(segment[6:]); orientation >= 1 && orientation <= 8 {
				return orientation
			}
		}
		offset += segmentLength
	}
	return 1
}

func parseTIFFOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	ifdOffset := uint64(order.Uint32(data[4:8]))
	if ifdOffset > uint64(len(data)-2) {
		return 1
	}
	entryCount := int(order.Uint16(data[ifdOffset : ifdOffset+2]))
	entriesStart := ifdOffset + 2
	if uint64(entryCount) > uint64(len(data)-int(entriesStart))/12 {
		return 1
	}
	for i := 0; i < entryCount; i++ {
		entry := data[entriesStart+uint64(i*12) : entriesStart+uint64((i+1)*12)]
		if order.Uint16(entry[0:2]) != 0x0112 || order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			continue
		}
		return int(order.Uint16(entry[8:10]))
	}
	return 1
}
