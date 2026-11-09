package render

import (
	"testing"

	"github.com/lathe-cli/lathe/internal/overlay"
	"github.com/lathe-cli/lathe/internal/testutil"
	"github.com/lathe-cli/lathe/pkg/runtime"
)

func TestArrayCountColumnConfiguration(t *testing.T) {
	specs := []runtime.CommandSpec{{Use: "list", Output: runtime.OutputHints{DefaultColumns: []string{"deployments"}}}}
	validate := func(format overlay.ColumnFormatOverride) error {
		return ValidateOverlayModule(specs, overlay.Module{Commands: map[string]overlay.Override{
			"list": {Output: &overlay.OutputOverride{ColumnFormats: map[string]overlay.ColumnFormatOverride{"deployments": format}}},
		}})
	}
	testutil.NoError(t, validate(overlay.ColumnFormatOverride{Kind: "count"}))
	for _, format := range []overlay.ColumnFormatOverride{
		{Kind: "count", Currency: "USD"},
		{Kind: "count", SourceScale: 1},
		{Kind: "count", Grouping: true},
		{Kind: "count", MinFractionDigits: 1},
		{Kind: "count", MaxFractionDigits: 1},
	} {
		testutil.Require(t, validate(format) != nil, "accepted currency options on count: %#v", format)
	}
}
