package sdk

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/marcelsud/keel/internal/protocol"
)

type KV struct {
	app       *App
	namespace string
}

func (k *KV) Namespace(name string) *KV { return &KV{app: k.app, namespace: name} }

// Get distinguishes a missing key (false) from a stored JSON null (true).
func (k *KV) Get(ctx context.Context, key string, output any) (bool, error) {
	var result protocol.Value
	if err := k.app.core(ctx, "kv_get", protocol.Request{Namespace: k.namespace, Key: key}, &result); err != nil {
		return false, err
	}
	if !result.Found {
		return false, nil
	}
	return true, json.Unmarshal(result.Value, output)
}

func (k *KV) Set(ctx context.Context, key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return k.app.core(ctx, "kv_set", protocol.Request{Namespace: k.namespace, Key: key, Value: data}, nil)
}

// GetEntry decodes a value and returns its revision, or zero when the key is missing.
func (k *KV) GetEntry(ctx context.Context, key string, output any) (uint64, error) {
	var result protocol.Value
	if err := k.app.core(ctx, "kv_get", protocol.Request{Namespace: k.namespace, Key: key}, &result); err != nil {
		return 0, err
	}
	if !result.Found {
		return 0, nil
	}
	return result.Revision, json.Unmarshal(result.Value, output)
}

// CompareAndSet writes only if revision matches. Zero creates a missing key.
// A conflicting write returns a 409 service error; callers must reread before retrying.
func (k *KV) CompareAndSet(ctx context.Context, key string, value any, revision uint64) (uint64, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return 0, err
	}
	var next uint64
	err = k.app.core(ctx, "kv_cas", protocol.Request{Namespace: k.namespace, Key: key, Value: data, Revision: revision}, &next)
	return next, err
}

func (k *KV) Delete(ctx context.Context, key string) (bool, error) {
	var removed bool
	err := k.app.core(ctx, "kv_delete", protocol.Request{Namespace: k.namespace, Key: key}, &removed)
	return removed, err
}

type Collections struct{ app *App }

type CreateOptions struct {
	// IfNeverExisted also rejects IDs retained as deletion tombstones.
	IfNeverExisted bool
}

// Create inserts a stable document ID without replacing an existing document.
func (c *Collections) Create(ctx context.Context, name, id string, document any, options ...CreateOptions) (string, error) {
	if id == "" {
		return "", protocol.BadRequest("document id must not be empty")
	}
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	var result string
	if len(options) > 1 {
		return "", protocol.BadRequest("at most one create options value is allowed")
	}
	ifNeverExisted := len(options) == 1 && options[0].IfNeverExisted
	err = c.app.core(ctx, "collection_insert", protocol.Request{Collection: name, ID: id, Document: data, IfNeverExisted: ifNeverExisted}, &result)
	return result, err
}

// List decodes documents in lexicographic ID order. Listings are bounded to
// 1000 documents / 512 KiB and are not a transaction across documents.
func (c *Collections) List(ctx context.Context, name string, output any) error {
	return c.app.core(ctx, "collection_list", protocol.Request{Collection: name}, output)
}

func (c *Collections) Insert(ctx context.Context, name string, document any) (string, error) {
	data, err := json.Marshal(document)
	if err != nil {
		return "", err
	}
	var id string
	err = c.app.core(ctx, "collection_insert", protocol.Request{Collection: name, Document: data}, &id)
	return id, err
}

func (c *Collections) Get(ctx context.Context, name, id string, output any) (bool, error) {
	var data json.RawMessage
	if err := c.app.core(ctx, "collection_get", protocol.Request{Collection: name, ID: id}, &data); err != nil {
		return false, err
	}
	if bytes.Equal(data, []byte("null")) {
		return false, nil
	}
	return true, json.Unmarshal(data, output)
}

func (c *Collections) Update(ctx context.Context, name, id string, patch any) (bool, error) {
	data, err := json.Marshal(patch)
	if err != nil {
		return false, err
	}
	var updated bool
	err = c.app.core(ctx, "collection_update", protocol.Request{Collection: name, ID: id, Document: data}, &updated)
	return updated, err
}

func (c *Collections) Delete(ctx context.Context, name, id string) (bool, error) {
	var removed bool
	err := c.app.core(ctx, "collection_delete", protocol.Request{Collection: name, ID: id}, &removed)
	return removed, err
}

type Pubsub struct{ app *App }

func (p *Pubsub) Publish(ctx context.Context, topic string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.app.core(ctx, "publish", protocol.Request{Topic: topic, Payload: data}, nil)
}

type Queue struct{ app *App }

func (q *Queue) Enqueue(ctx context.Context, queue string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return q.app.core(ctx, "enqueue", protocol.Request{Topic: queue, Payload: data}, nil)
}
