package mlog

import (
	"fmt"
	"reflect"
)

// SafeFormatter creates bounded summaries. It is NOT a lock or a data snapshot.
// Callers must synchronize mutable slices, pointers, structs and Error methods.
// Maps are represented by their type only; their contents/length are not read.
type SafeFormatter struct{}

func NewSafeFormatter() *SafeFormatter { return &SafeFormatter{} }

func (sf *SafeFormatter) FormatSafely(format string, args ...interface{}) string {
	if len(args) == 0 {
		return format
	}

	safeArgs := make([]interface{}, len(args))
	for i, arg := range args {
		safeArgs[i] = sf.makeArgSafe(arg)
	}

	return fmt.Sprintf(format, safeArgs...)
}

func (sf *SafeFormatter) makeArgSafe(arg interface{}) interface{} {
	remaining := 1024
	return sf.makeArgSafeDepth(arg, 0, &remaining)
}
func (sf *SafeFormatter) makeArgSafeDepth(arg interface{}, depth int, remaining *int) (result interface{}) {
	if *remaining <= 0 {
		return "<max-items>"
	}
	*remaining--
	if depth >= 32 {
		return "<max-depth>"
	}
	defer func() {
		if r := recover(); r != nil {
			result = "<format panic>"
		}
	}()
	if arg == nil {
		return nil
	}

	switch v := arg.(type) {
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, complex64, complex128,
		string:
		return v
	case []byte:
		copied := make([]byte, len(v))
		copy(copied, v)
		return copied
	case error:
		return v.Error()
	}

	return sf.makeComplexArgSafeDepth(arg, depth, remaining)
}

func (sf *SafeFormatter) makeComplexArgSafeDepth(arg interface{}, depth int, remaining *int) interface{} {
	val := reflect.ValueOf(arg)

	if !val.IsValid() {
		return nil
	}

	switch val.Kind() {
	case reflect.Ptr:
		if val.IsNil() {
			return nil
		}
		return sf.makeArgSafeDepth(val.Elem().Interface(), depth+1, remaining)

	case reflect.Map:
		// Map contents and length are deliberately omitted.
		return sf.mapToSafeString(val)

	case reflect.Slice, reflect.Array:
		return sf.sliceToSafe(val, depth, remaining)

	case reflect.Struct:
		return sf.structToSafeMap(val, depth, remaining)

	case reflect.Chan, reflect.Func:
		return fmt.Sprintf("<%s>", val.Type().String())

	default:
		return fmt.Sprintf("%v", arg)
	}
}

// mapToSafeString intentionally does not inspect map contents or length.
func (sf *SafeFormatter) mapToSafeString(val reflect.Value) string {
	if val.IsNil() {
		return "nil"
	}
	return "<" + val.Type().String() + ">"
}

func (sf *SafeFormatter) sliceToSafe(val reflect.Value, depth int, remaining *int) interface{} {
	if val.Kind() == reflect.Slice && val.IsNil() {
		return nil
	}

	length := val.Len()

	if length <= 10 {
		result := make([]interface{}, length)
		for i := 0; i < length; i++ {
			result[i] = sf.makeArgSafeDepth(val.Index(i).Interface(), depth+1, remaining)
		}
		return result
	}

	return fmt.Sprintf("[%d items of %s]", length, val.Type().Elem().String())
}

func (sf *SafeFormatter) structToSafeMap(val reflect.Value, depth int, remaining *int) interface{} {
	typ := val.Type()
	result := make(map[string]interface{})

	for i := 0; i < val.NumField(); i++ {
		field := typ.Field(i)

		if field.PkgPath != "" {
			continue
		}

		fieldVal := val.Field(i)

		if isZeroValue(fieldVal) {
			continue
		}

		result[field.Name] = sf.makeArgSafeDepth(fieldVal.Interface(), depth+1, remaining)
	}

	return result
}

func isZeroValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr, reflect.Map:
		return v.IsNil()
	case reflect.Struct:
		return false
	default:
		return false
	}
}

var globalSafeFormatter = NewSafeFormatter()

func SafeFormat(format string, args ...interface{}) string {
	return globalSafeFormatter.FormatSafely(format, args...)
}
