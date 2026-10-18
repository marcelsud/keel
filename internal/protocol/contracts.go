package protocol

import (
	"crypto/sha256"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
)

const Active = "active"
const Deprecated = "deprecated"
const Retired = "retired"

type Schema struct {
	Service       string `json:"service"`
	SHA256        string `json:"sha256"`
	State         string `json:"state"`
	Replacement   string `json:"replacement,omitempty"`
	DescriptorSet []byte `json:"descriptorSet"`
}

func QualifiedName(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if !protoreflect.Name(part).IsValid() {
			return false
		}
	}
	return true
}

func SchemaSubject(service string) string      { return Prefix + ".schema." + service }
func RPCSubject(service, method string) string { return Prefix + ".rpc." + service + "." + method }
func Digest(data []byte) string                { return fmt.Sprintf("%x", sha256.Sum256(data)) }

// Descriptor validates the complete dependency closure without registering global types.
func Descriptor(data []byte, service string) (protoreflect.ServiceDescriptor, *descriptorpb.FileDescriptorSet, error) {
	if !QualifiedName(service) {
		return nil, nil, fmt.Errorf("invalid contract name %q", service)
	}
	set := new(descriptorpb.FileDescriptorSet)
	if err := proto.Unmarshal(data, set); err != nil {
		return nil, nil, fmt.Errorf("invalid descriptor set: %w", err)
	}
	files, err := protodesc.NewFiles(set)
	if err != nil {
		return nil, nil, err
	}
	descriptor, err := files.FindDescriptorByName(protoreflect.FullName(service))
	if err != nil {
		return nil, nil, err
	}
	result, ok := descriptor.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a service", service)
	}
	for i := 0; i < result.Methods().Len(); i++ {
		method := result.Methods().Get(i)
		if method.IsStreamingClient() || method.IsStreamingServer() {
			return nil, nil, fmt.Errorf("streaming RPC %s is not supported", method.FullName())
		}
	}
	return result, set, nil
}
