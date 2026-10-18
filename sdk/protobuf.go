package sdk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/marcelsud/keel/internal/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const Active = protocol.Active
const Deprecated = protocol.Deprecated
const Retired = protocol.Retired

// Contract is generated alongside the protobuf messages and contains all imported schemas.
type Contract struct {
	Service string
	Schema  string // Base64-encoded FileDescriptorSet, identical across SDK languages.
}

type ContractOptions struct{ State, Replacement string }
type Schema = protocol.Schema

// ProtobufCaller is implemented by both App and the handler Context.
type ProtobufCaller interface {
	CallProtobuf(context.Context, string, string, proto.Message, proto.Message) error
}

func (c *Context) CallProtobuf(ctx context.Context, service, method string, input, output proto.Message) error {
	return c.app.CallProtobuf(ctx, service, method, input, output)
}

type ProtobufMethod struct {
	NewInput func() proto.Message
	Handler  func(*Context, proto.Message) (proto.Message, error)
}

// RegisterProtobuf is called by the generated, typed service registration functions.
func (a *App) RegisterProtobuf(contract Contract, handlers map[string]ProtobufMethod, options ContractOptions) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started || a.stopped {
		return errors.New("declare services before start")
	}
	service := contract.Service
	data, err := base64.StdEncoding.DecodeString(contract.Schema)
	if err != nil {
		return fmt.Errorf("invalid embedded contract: %w", err)
	}
	descriptor, _, err := protocol.Descriptor(data, service)
	if err != nil {
		return err
	}
	if options.State == "" {
		options.State = Active
		if deprecated, ok := descriptor.Options().(*descriptorpb.ServiceOptions); ok && deprecated.GetDeprecated() {
			options.State = Deprecated
		}
	}
	if options.State != Active && options.State != Deprecated && options.State != Retired {
		return errors.New("invalid contract state")
	}
	if options.Replacement != "" && !protocol.QualifiedName(options.Replacement) {
		return errors.New("replacement must be a qualified contract name")
	}
	schema := Schema{Service: service, SHA256: protocol.Digest(data), State: options.State, Replacement: options.Replacement, DescriptorSet: data}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return err
	}
	metadata := map[string]string{"contract": service, "state": options.State, "replacement": options.Replacement, "sha256": schema.SHA256}
	routes := map[string]endpoint{
		"schema_" + protocol.Encode(service): {subject: protocol.SchemaSubject(service), metadata: metadata, handler: func(*Context, []byte) ([]byte, error) { return encoded, nil }},
	}
	for i := 0; i < descriptor.Methods().Len(); i++ {
		method := descriptor.Methods().Get(i)
		name := string(method.Name())
		handler, ok := handlers[name]
		if options.State != Retired && (!ok || handler.NewInput == nil || handler.Handler == nil) {
			return fmt.Errorf("missing handler for %s", method.FullName())
		}
		if options.State != Retired {
			input := handler.NewInput()
			if input == nil || input.ProtoReflect().Descriptor().FullName() != method.Input().FullName() {
				return fmt.Errorf("wrong input type for %s", method.FullName())
			}
		}
		routes["rpc_"+protocol.Encode(service+"."+name)] = endpoint{
			subject: protocol.RPCSubject(service, name), metadata: metadata,
			handler: func(ctx *Context, data []byte) ([]byte, error) {
				if options.State == Retired {
					return nil, &Error{Code: 410, Message: "API retired; replacement: " + options.Replacement}
				}
				input := handler.NewInput()
				if err := proto.Unmarshal(data, input); err != nil {
					return nil, &Error{Code: 400, Message: "invalid protobuf request"}
				}
				output, err := handler.Handler(ctx, input)
				if err != nil {
					return nil, err
				}
				if output == nil || !output.ProtoReflect().IsValid() || output.ProtoReflect().Descriptor().FullName() != method.Output().FullName() {
					return nil, errors.New("handler returned an invalid protobuf response")
				}
				return proto.Marshal(output)
			},
		}
	}
	for name := range routes {
		if _, exists := a.endpoints[name]; exists {
			return fmt.Errorf("duplicate service endpoint: %s", service)
		}
	}
	for name, route := range routes {
		a.endpoints[name] = route
	}
	return nil
}

// CallProtobuf is the binary transport used by generated clients.
func (a *App) CallProtobuf(ctx context.Context, service, method string, input, output proto.Message) error {
	if !protocol.QualifiedName(service) || !protoreflect.Name(method).IsValid() {
		return errors.New("invalid contract or method name")
	}
	if input == nil || output == nil || !input.ProtoReflect().IsValid() || !output.ProtoReflect().IsValid() {
		return errors.New("protobuf messages must not be nil")
	}
	data, err := proto.Marshal(input)
	if err != nil {
		return err
	}
	data, err = a.requestBytes(ctx, protocol.RPCSubject(service, method), data)
	if err != nil {
		return err
	}
	return proto.Unmarshal(data, output)
}

// FetchContract obtains metadata and descriptors in one reply, avoiding a moving-alias race.
func (a *App) FetchContract(ctx context.Context, service string) (Schema, error) {
	var schema Schema
	if !protocol.QualifiedName(service) {
		return schema, errors.New("invalid contract name")
	}
	if err := a.request(ctx, protocol.SchemaSubject(service), nil, &schema); err != nil {
		return schema, err
	}
	if schema.Service != service || schema.SHA256 != protocol.Digest(schema.DescriptorSet) {
		return schema, errors.New("contract identity or checksum mismatch")
	}
	if schema.State != Active && schema.State != Deprecated && schema.State != Retired {
		return schema, errors.New("invalid contract state")
	}
	if schema.Replacement != "" && !protocol.QualifiedName(schema.Replacement) {
		return schema, errors.New("invalid replacement contract")
	}
	_, _, err := protocol.Descriptor(schema.DescriptorSet, service)
	return schema, err
}
