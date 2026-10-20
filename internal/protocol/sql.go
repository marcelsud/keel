package protocol

import "encoding/json"

func SQLSubject(service, operation string) string {
	return Prefix + ".sql." + service + "." + operation
}

type SQLRequest struct {
	Statement   string            `json:"statement,omitempty"`
	Params      []json.RawMessage `json:"params,omitempty"`
	Transaction string            `json:"transaction,omitempty"`
	Migrations  []Migration       `json:"migrations,omitempty"`
}

type SQLResult struct {
	Rows     []map[string]any `json:"rows"`
	RowCount int64            `json:"rowCount"`
}

type SQLTransaction struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	ExpiresAt int64  `json:"expiresAt"`
}

type Migration struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}
