package main

import (
	"errors"
	"math"
	"strconv"
	"strings"
)

var errMalformedRange = errors.New("malformed range")
var errUnsatisfiableRange = errors.New("unsatisfiable range")

type byteRange struct{ start, end int64 } // inclusive

func parseRange(header string, size int64) (byteRange, error) {
	if len(header) < 6 || !strings.EqualFold(header[:6], "bytes=") || strings.Contains(header, ",") {
		return byteRange{}, errMalformedRange
	}
	spec := header[6:]
	if strings.Count(spec, "-") != 1 {
		return byteRange{}, errMalformedRange
	}
	parts := strings.SplitN(spec, "-", 2)
	if parts[0] == "" && parts[1] == "" {
		return byteRange{}, errMalformedRange
	}
	for _, part := range parts {
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return byteRange{}, errMalformedRange
			}
		}
	}
	if size == 0 {
		return byteRange{}, errUnsatisfiableRange
	}
	if parts[0] == "" {
		length := rangeNumber(parts[1])
		if length == 0 {
			return byteRange{}, errUnsatisfiableRange
		}
		if length > size {
			length = size
		}
		return byteRange{size - length, size - 1}, nil
	}
	start := rangeNumber(parts[0])
	if start >= size {
		return byteRange{}, errUnsatisfiableRange
	}
	end := size - 1
	if parts[1] != "" {
		end = rangeNumber(parts[1])
		if end < start {
			return byteRange{}, errUnsatisfiableRange
		}
		if end >= size {
			end = size - 1
		}
	}
	return byteRange{start, end}, nil
}

// Digits are validated before this call. Valid arbitrarily large numerals
// saturate; offsets and file sizes cannot exceed int64 on this server.
func rangeNumber(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return math.MaxInt64
	}
	return n
}
