package runtime

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"strconv"
	"strings"
)

func decodeNumberPreserving(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var v any
	if err := decoder.Decode(&v); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("trailing data after JSON value")
	}
	return v, nil
}

func formatColumnValue(value any, format ColumnFormat) (string, bool) {
	if format.Kind != "currency" || !validColumnFormat(format) {
		return "", false
	}
	negative, digits, ok := integerValue(value)
	if !ok {
		return "", false
	}
	if strings.Trim(digits, "0") == "" {
		negative = false
	}
	integer, fraction := scaleDigits(digits, format.SourceScale)
	for len(fraction) > format.MinFractionDigits && strings.HasSuffix(fraction, "0") {
		fraction = fraction[:len(fraction)-1]
	}
	for len(fraction) < format.MinFractionDigits {
		fraction += "0"
	}
	if format.Grouping {
		integer = groupInteger(integer)
	}
	prefix := format.Currency + " "
	if format.Currency == "USD" {
		prefix = "$"
	}
	var result strings.Builder
	if negative {
		result.WriteByte('-')
	}
	result.WriteString(prefix)
	result.WriteString(integer)
	if fraction != "" {
		result.WriteByte('.')
		result.WriteString(fraction)
	}
	return result.String(), true
}

func validColumnFormat(format ColumnFormat) bool {
	if len(format.Currency) != 3 {
		return false
	}
	for _, r := range format.Currency {
		if r < 'A' || r > 'Z' {
			return false
		}
	}
	return format.SourceScale >= 0 && format.SourceScale <= 18 &&
		format.MinFractionDigits >= 0 && format.MinFractionDigits <= 18 &&
		format.MaxFractionDigits >= format.MinFractionDigits && format.MaxFractionDigits <= 18 &&
		format.MaxFractionDigits >= format.SourceScale
}

func integerValue(value any) (bool, string, bool) {
	var raw string
	switch value := value.(type) {
	case json.Number:
		raw = value.String()
	case string:
		raw = value
	case int:
		raw = strconv.Itoa(value)
	case int8:
		raw = strconv.FormatInt(int64(value), 10)
	case int16:
		raw = strconv.FormatInt(int64(value), 10)
	case int32:
		raw = strconv.FormatInt(int64(value), 10)
	case int64:
		raw = strconv.FormatInt(value, 10)
	case uint:
		raw = strconv.FormatUint(uint64(value), 10)
	case uint8:
		raw = strconv.FormatUint(uint64(value), 10)
	case uint16:
		raw = strconv.FormatUint(uint64(value), 10)
	case uint32:
		raw = strconv.FormatUint(uint64(value), 10)
	case uint64:
		raw = strconv.FormatUint(value, 10)
	default:
		return false, "", false
	}
	return parseInteger(raw)
}

func parseInteger(raw string) (bool, string, bool) {
	if raw == "" || len(raw) > 4096 || strings.TrimSpace(raw) != raw || !json.Valid([]byte(raw)) {
		return false, "", false
	}
	if _, err := strconv.ParseFloat(raw, 64); err != nil {
		return false, "", false
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok || !value.IsInt() {
		return false, "", false
	}
	integer := value.Num()
	return integer.Sign() < 0, new(big.Int).Abs(integer).String(), true
}

func scaleDigits(digits string, scale int) (string, string) {
	if scale == 0 {
		return digits, ""
	}
	if len(digits) <= scale {
		return "0", strings.Repeat("0", scale-len(digits)) + digits
	}
	return digits[:len(digits)-scale], digits[len(digits)-scale:]
}

func groupInteger(value string) string {
	if len(value) <= 3 {
		return value
	}
	first := len(value) % 3
	if first == 0 {
		first = 3
	}
	var result strings.Builder
	result.Grow(len(value) + len(value)/3)
	result.WriteString(value[:first])
	for index := first; index < len(value); index += 3 {
		result.WriteByte(',')
		result.WriteString(value[index : index+3])
	}
	return result.String()
}
