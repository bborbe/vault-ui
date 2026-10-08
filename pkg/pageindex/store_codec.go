// Copyright (c) 2026 Benjamin Borbe All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package pageindex

import (
	"bytes"
	"context"
	"encoding/gob"
	"time"

	"github.com/bborbe/errors"
	libtime "github.com/bborbe/time"
	"github.com/bborbe/vault-cli/pkg/domain"
)

// storeRecord is the on-disk shape of one stored entry. It is the gob codec's
// own type: domain.Page is deliberately not encoded directly, because its
// embedded FrontmatterMap has only an unexported field and both gob and JSON
// would silently drop every frontmatter key. The page is rebuilt from
// FrontmatterMap.RawMap() on the way in and domain.NewPage on the way out.
type storeRecord struct {
	Filename    string
	HasPage     bool
	Frontmatter map[string]any
	Metadata    domain.FileMetadata
	Content     string
	Fingerprint FileFingerprint
}

// init registers every concrete type that can appear as a value in a decoded
// YAML frontmatter map. gob encodes interface values with their concrete type
// name, so an unregistered value makes the encode fail. The list is the YAML
// decoder's value set: scalars, sequences, string-keyed maps, and the generic
// map[any]any a nested mapping with non-string keys produces. A new type in a
// fixture fails the codec test loudly; extend this list rather than dropping
// the value.
//
// libtime.DateOrDateTime is deliberately NOT registered: it implements
// BinaryMarshaler but not BinaryUnmarshaler, so gob encodes it and then fails
// to decode. encodeValue converts it to time.Time first.
func init() {
	gob.Register("")
	gob.Register(false)
	gob.Register(int(0))
	gob.Register(int64(0))
	gob.Register(float64(0))
	gob.Register(time.Time{})
	gob.Register([]any{})
	gob.Register(map[string]any{})
	gob.Register(map[any]any{})
}

// encodeRecord encodes one entry into its stored bytes.
func encodeRecord(ctx context.Context, entry StoredEntry) ([]byte, error) {
	record := storeRecord{
		Filename:    entry.Filename,
		Fingerprint: entry.Fingerprint,
	}
	if entry.Page != nil {
		converted, err := encodeValue(ctx, entry.Page.RawMap())
		if err != nil {
			return nil, err
		}
		frontmatter, ok := converted.(map[string]any)
		if !ok {
			return nil, errors.Errorf(
				ctx,
				"encode page index record %s: unexpected frontmatter type %T",
				entry.Filename,
				converted,
			)
		}
		record.HasPage = true
		record.Frontmatter = frontmatter
		record.Metadata = entry.Page.FileMetadata
		record.Content = string(entry.Page.Content)
	}

	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(record); err != nil {
		return nil, errors.Wrapf(ctx, err, "encode page index record %s", entry.Filename)
	}
	return buffer.Bytes(), nil
}

// decodeRecord decodes one entry's stored bytes. A decode error is returned,
// never a partially-filled record.
func decodeRecord(ctx context.Context, data []byte) (StoredEntry, error) {
	var record storeRecord
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&record); err != nil {
		return StoredEntry{}, errors.Wrap(ctx, err, "decode page index record")
	}

	entry := StoredEntry{
		Filename:    record.Filename,
		Fingerprint: record.Fingerprint,
	}
	if record.HasPage {
		entry.Page = domain.NewPage(record.Frontmatter, record.Metadata, domain.Content(record.Content))
	}
	return entry, nil
}

// encodeValue converts a frontmatter value into a gob-encodable one. Only
// libtime.DateOrDateTime needs rewriting: gob cannot decode it, so it becomes
// the time.Time it wraps. Maps and slices are walked so a nested value is
// converted too; every other value is returned unchanged.
func encodeValue(ctx context.Context, value any) (any, error) {
	switch typed := value.(type) {
	case libtime.DateOrDateTime:
		return typed.Time(), nil
	case *libtime.DateOrDateTime:
		if typed == nil {
			return nil, nil
		}
		return typed.Time(), nil
	case map[string]any:
		converted := make(map[string]any, len(typed))
		for key, item := range typed {
			value, err := encodeValue(ctx, item)
			if err != nil {
				return nil, err
			}
			converted[key] = value
		}
		return converted, nil
	case map[any]any:
		converted := make(map[any]any, len(typed))
		for key, item := range typed {
			value, err := encodeValue(ctx, item)
			if err != nil {
				return nil, err
			}
			converted[key] = value
		}
		return converted, nil
	case []any:
		converted := make([]any, len(typed))
		for index, item := range typed {
			value, err := encodeValue(ctx, item)
			if err != nil {
				return nil, err
			}
			converted[index] = value
		}
		return converted, nil
	default:
		return value, nil
	}
}
