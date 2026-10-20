package catalog

import (
	"context"
	"github.com/marcelsud/keel/examples/cinema/packages/shared-go"
	"github.com/marcelsud/keel/sdk"
)

type Client struct{ App *sdk.App }

func (c *Client) GetScreening(ctx context.Context, id string) (cinema.Screening, error) {
	var screening cinema.Screening
	err := c.App.Call(ctx, "cinema-catalog", "getScreening", map[string]string{"screeningId": id}, &screening)
	return screening, err
}
func (c *Client) ListScreenings(ctx context.Context) ([]cinema.Screening, error) {
	var screenings []cinema.Screening
	err := c.App.Call(ctx, "cinema-catalog", "listScreenings", map[string]any{}, &screenings)
	return screenings, err
}
