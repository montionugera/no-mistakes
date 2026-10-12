package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kunchenguid/no-mistakes/internal/types"
)

const shippingPushPrefix = "shipping="

func formatShippingPushOption(s *types.ShippingOptions) string {
	if s == nil {
		return ""
	}
	b, _ := json.Marshal(s)
	return shippingPushPrefix + base64.StdEncoding.EncodeToString(b)
}
func parseShippingPushOptions(options []string) (*types.ShippingOptions, error) {
	var found *types.ShippingOptions
	var prior string
	for _, opt := range options {
		if !strings.HasPrefix(opt, shippingPushPrefix) {
			continue
		}
		v := strings.TrimPrefix(opt, shippingPushPrefix)
		if prior != "" && prior != v {
			return nil, fmt.Errorf("conflicting shipping push options")
		}
		prior = v
		b, err := base64.StdEncoding.DecodeString(v)
		if err != nil || len(b) > 1024 {
			return nil, fmt.Errorf("invalid shipping push option")
		}
		var s types.ShippingOptions
		if err := json.Unmarshal(b, &s); err != nil {
			return nil, fmt.Errorf("invalid shipping push option")
		}
		if err := s.Validate(); err != nil {
			return nil, err
		}
		found = &s
	}
	return found, nil
}
