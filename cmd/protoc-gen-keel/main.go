package main

import (
	"io"
	"log"
	"os"

	"github.com/marcelsud/keel/internal/codegen"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/pluginpb"
)

func main() {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		log.Fatal(err)
	}
	request := new(pluginpb.CodeGeneratorRequest)
	if err = proto.Unmarshal(data, request); err != nil {
		log.Fatal(err)
	}
	response, err := codegen.Generate(request)
	if err != nil {
		response = &pluginpb.CodeGeneratorResponse{Error: proto.String(err.Error())}
	}
	data, err = proto.Marshal(response)
	if err != nil {
		log.Fatal(err)
	}
	if _, err = os.Stdout.Write(data); err != nil {
		log.Fatal(err)
	}
}
