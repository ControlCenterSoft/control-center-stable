package recovery

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
)

const maxMetadataDocumentBytes = 1 << 20

func DecodeRecoveryPoint(raw []byte) (RecoveryPoint, error) {
	return decodeStrict(raw, "recovery point", ValidateRecoveryPoint)
}

func DecodeProviderMetadata(raw []byte) (ProviderMetadata, error) {
	return decodeStrict(raw, "provider metadata", ValidateProviderMetadata)
}

func DecodeFencingMetadata(raw []byte) (FencingMetadata, error) {
	return decodeStrict(raw, "fencing metadata", ValidateFencingMetadata)
}

func DecodeBackupMetadata(raw []byte) (BackupMetadata, error) {
	return decodeStrict(raw, "backup metadata", ValidateBackupMetadata)
}

func DecodeRestoreMetadata(raw []byte) (RestoreMetadata, error) {
	return decodeStrict(raw, "restore metadata", ValidateRestoreMetadata)
}

func DecodeRecoveryObjectiveEvidence(raw []byte) (RecoveryObjectiveEvidence, error) {
	return decodeStrict(raw, "recovery objective evidence", ValidateRecoveryObjectiveEvidence)
}

func decodeStrict[T any](raw []byte, name string, validate func(T) error) (T, error) {
	var zero T
	if len(raw) == 0 || len(raw) > maxMetadataDocumentBytes {
		return zero, fmt.Errorf("decode %s: document size must be between 1 and %d bytes", name, maxMetadataDocumentBytes)
	}
	if err := rejectDuplicateFields(raw); err != nil {
		return zero, fmt.Errorf("decode %s: %w", name, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var value T
	if err := decoder.Decode(&value); err != nil {
		return zero, fmt.Errorf("decode %s: %w", name, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return zero, fmt.Errorf("decode %s: multiple JSON values are not allowed", name)
		}
		return zero, fmt.Errorf("decode %s: trailing data: %w", name, err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return zero, fmt.Errorf("decode %s: inspect JSON shape: %w", name, err)
	}
	if _, ok := document.(map[string]any); !ok {
		return zero, fmt.Errorf("decode %s: top-level JSON value must be an object", name)
	}
	if err := validateJSONShape(document, reflect.TypeOf(value), ""); err != nil {
		return zero, err
	}
	if err := validate(value); err != nil {
		return zero, err
	}
	return value, nil
}

// validateJSONShape makes the Go decoder enforce the same presence and null
// rules as the OpenAPI schemas. encoding/json otherwise cannot distinguish an
// omitted required bool (for example failure.retryable) from an explicit false,
// and treats null optional pointers as if their property were absent.
func validateJSONShape(value any, expected reflect.Type, path string) error {
	for expected.Kind() == reflect.Pointer {
		expected = expected.Elem()
	}

	switch expected.Kind() {
	case reflect.Struct:
		fields := make([]reflect.StructField, 0, expected.NumField())
		for index := 0; index < expected.NumField(); index++ {
			field := expected.Field(index)
			if field.PkgPath != "" {
				continue
			}
			tag := field.Tag.Get("json")
			if tag == "-" {
				continue
			}
			fields = append(fields, field)
		}
		// Scalar structs such as time.Time have no JSON-tagged fields.
		if len(fields) == 0 {
			return nil
		}
		object, ok := value.(map[string]any)
		if !ok {
			return invalid(pathOrRoot(path), "must be an object")
		}
		for _, field := range fields {
			if field.Anonymous && field.Tag.Get("json") == "" {
				if err := validateJSONShape(value, field.Type, path); err != nil {
					return err
				}
				continue
			}
			tagParts := strings.Split(field.Tag.Get("json"), ",")
			name := tagParts[0]
			if name == "" {
				name = field.Name
			}
			optional := false
			for _, option := range tagParts[1:] {
				optional = optional || option == "omitempty"
			}
			fieldPath := joinJSONPath(path, name)
			child, exists := object[name]
			if !exists {
				if optional {
					continue
				}
				return invalid(fieldPath, "field is required")
			}
			if child == nil {
				return invalid(fieldPath, "must not be null")
			}
			if err := validateJSONShape(child, field.Type, fieldPath); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		items, ok := value.([]any)
		if !ok {
			return invalid(pathOrRoot(path), "must be an array")
		}
		for index, item := range items {
			itemPath := fmt.Sprintf("%s[%d]", pathOrRoot(path), index)
			if item == nil {
				return invalid(itemPath, "must not be null")
			}
			if err := validateJSONShape(item, expected.Elem(), itemPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func joinJSONPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func pathOrRoot(path string) string {
	if path == "" {
		return "$"
	}
	return path
}

func rejectDuplicateFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSON(decoder, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func walkJSON(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("object key at %s is not a string", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate field %q at %s", key, path)
			}
			seen[key] = struct{}{}
			if err := walkJSON(decoder, path+"."+key); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("invalid object closing token at %s", path)
		}
	case '[':
		for index := 0; decoder.More(); index++ {
			if err := walkJSON(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("invalid array closing token at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q at %s", delimiter, path)
	}
	return nil
}
