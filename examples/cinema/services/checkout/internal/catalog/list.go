package catalog

import (
	"encoding/json"
	"github.com/marcelsud/keel/sdk"
)

func Register(app *sdk.App) error {
	return app.Action("list", func(ctx *sdk.Context, raw json.RawMessage) (any, error) {
		var result json.RawMessage
		err := ctx.Call("cinema-catalog", "list", map[string]any{}, &result)
		return result, err
	})
}
