package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"reflect"
	"strconv"
	"strings"
)

// DecodeJSONNumbers preserves flexible JSON values while callers continue to
// validate their own operational fields at the request or import boundary.
func DecodeJSONNumbers(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("expected one JSON value")
	}
	return nil
}

// EqualJSONNumbers compares JSON meaning without rounding numerical evidence.
// Coefficients and decimal exponents stay separate; huge exponents never expand
// into huge powers or zero-filled strings just to compare two archive entries.
func EqualJSONNumbers(left, right any) bool {
	if number, ok := jsonNumberText(left); ok {
		other, ok := jsonNumberText(right)
		if !ok {
			return false
		}
		a, x, ok := decimalParts(number)
		if !ok {
			return false
		}
		b, y, ok := decimalParts(other)
		return ok && a == b && x.Cmp(y) == 0
	}
	switch value := left.(type) {
	case map[string]any:
		other, ok := right.(map[string]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for key, item := range value {
			candidate, exists := other[key]
			if !exists || !EqualJSONNumbers(item, candidate) {
				return false
			}
		}
		return true
	case []any:
		other, ok := right.([]any)
		if !ok || len(value) != len(other) {
			return false
		}
		for i, item := range value {
			if !EqualJSONNumbers(item, other[i]) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(left, right)
	}
}

func jsonNumberText(value any) (string, bool) {
	switch value := value.(type) {
	case json.Number:
		return string(value), true
	case float64:
		return strconv.FormatFloat(value, 'g', -1, 64), true
	case int:
		return strconv.Itoa(value), true
	case int64:
		return strconv.FormatInt(value, 10), true
	default:
		return "", false
	}
}

func decimalParts(number string) (string, *big.Int, bool) {
	negative := strings.HasPrefix(number, "-")
	number = strings.TrimPrefix(number, "-")
	exponent := new(big.Int)
	if at := strings.IndexAny(number, "eE"); at >= 0 {
		if _, ok := exponent.SetString(number[at+1:], 10); !ok {
			return "", nil, false
		}
		number = number[:at]
	}
	fraction := 0
	if at := strings.IndexByte(number, '.'); at >= 0 {
		fraction = len(number) - at - 1
		number = number[:at] + number[at+1:]
	}
	if number == "" || strings.IndexFunc(number, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return "", nil, false
	}
	number = strings.TrimLeft(number, "0")
	if number == "" {
		return "0", new(big.Int), true
	}
	coefficient := strings.TrimRight(number, "0")
	exponent.Add(exponent, big.NewInt(int64(len(number)-len(coefficient)-fraction)))
	if negative {
		coefficient = "-" + coefficient
	}
	return coefficient, exponent, true
}
