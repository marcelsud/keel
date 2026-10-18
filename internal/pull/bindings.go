package pull

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/marcelsud/keel/internal/codegen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/pluginpb"
)

// Bindings generates a complete bundle without writing any files.
func Bindings(ctx context.Context, set *descriptorpb.FileDescriptorSet, options Options) (map[string]string, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	request := &pluginpb.CodeGeneratorRequest{ProtoFile: set.File}
	for _, file := range set.File {
		if !safeName(file.GetName()) {
			return nil, fmt.Errorf("unsafe protobuf file path %q", file.GetName())
		}
		if !strings.HasPrefix(file.GetName(), "google/protobuf/") {
			request.FileToGenerate = append(request.FileToGenerate, file.GetName())
		}
	}
	outputs := make(map[string]string)
	languages := []string{options.Language}
	if options.Language == "both" {
		languages = []string{"go", "typescript"}
	}
	for _, language := range languages {
		parameter := "paths=source_relative"
		plugin := options.GoPlugin
		if language == "typescript" {
			parameter = "target=ts,import_extension=js"
			plugin = options.ESPlugin
		} else {
			for _, file := range request.FileToGenerate {
				parameter += ",M" + file + "=" + path.Join(options.GoPackagePrefix, path.Dir(file))
			}
		}
		messageRequest := proto.Clone(request).(*pluginpb.CodeGeneratorRequest)
		messageRequest.Parameter = proto.String(parameter)
		messages, err := runPlugin(ctx, plugin, messageRequest)
		if err != nil {
			return nil, err
		}
		bindingRequest := proto.Clone(messageRequest).(*pluginpb.CodeGeneratorRequest)
		if language == "typescript" {
			parameter = "paths=source_relative"
		}
		bindingRequest.Parameter = proto.String(parameter + ",lang=" + language)
		bindings, err := codegen.Generate(bindingRequest)
		if err != nil {
			return nil, err
		}
		for _, response := range []*pluginpb.CodeGeneratorResponse{messages, bindings} {
			if response.GetError() != "" {
				return nil, errors.New(response.GetError())
			}
			for _, file := range response.File {
				name := file.GetName()
				if !safeName(name) || file.GetInsertionPoint() != "" {
					return nil, fmt.Errorf("unsupported or unsafe generated file %q", name)
				}
				if options.Language == "both" {
					name = language + "/" + name
				}
				if _, exists := outputs[name]; exists {
					return nil, fmt.Errorf("duplicate generated file %q", name)
				}
				outputs[name] = file.GetContent()
			}
		}
	}
	return outputs, nil
}

// WriteFile atomically writes one generated file without following nested symlinks.
func WriteFile(root, name string, data []byte) error { return writeFile(root, name, data) }
