package receipts

import (
	"encoding/json"
	"github.com/marcelsud/keel/sdk"
)

func Register(app *sdk.App) error {
	return app.Action("receipts", func(ctx *sdk.Context, raw json.RawMessage) (any, error) {
		var result json.RawMessage
		err := ctx.Call("cinema-notifications", "getReceipts", raw, &result)
		return result, err
	})
}
