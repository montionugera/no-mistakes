package types

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
)

const ShippingFull = "full"
const ShippingImmutableSource = "immutable-source"

// ShippingOptions pins explicit strict shipping semantics for one durable run.
type ShippingOptions struct {
	Mode         string `json:"mode"`
	Draft        *bool  `json:"draft,omitempty"`
	TargetBranch string `json:"target_branch,omitempty"`
	TargetSHA    string `json:"target_sha,omitempty"`
}

func (s *ShippingOptions) Validate() error {
	if s != nil && s.Mode != ShippingFull && s.Mode != ShippingImmutableSource {
		return fmt.Errorf("shipping mode must be full or immutable-source")
	}
	return nil
}
func (s *ShippingOptions) Matches(request *ShippingOptions) bool {
	return request == nil || s != nil && s.Mode == request.Mode && (request.Draft == nil || s.Draft != nil && *s.Draft == *request.Draft)
}
func (s *ShippingOptions) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	if err := s.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(s)
	return string(b), err
}
func (s *ShippingOptions) Scan(src any) error {
	var data []byte
	switch v := src.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("invalid shipping options storage")
	}
	if err := json.Unmarshal(data, s); err != nil {
		return fmt.Errorf("invalid shipping options storage")
	}
	return s.Validate()
}

type shippingContextKey struct{}

func WithShippingOptions(ctx context.Context, options *ShippingOptions) context.Context {
	return context.WithValue(ctx, shippingContextKey{}, options)
}
func ShippingFromContext(ctx context.Context) *ShippingOptions {
	s, _ := ctx.Value(shippingContextKey{}).(*ShippingOptions)
	return s
}
