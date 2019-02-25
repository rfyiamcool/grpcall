package grpcall

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	"github.com/jhump/protoreflect/dynamic"
)

type Format string

const (
	FormatJSON = Format("json")
)

// RequestParser processes input into messages.
type RequestParser interface {
	// Next parses input data into the given request message. If called after
	// input is exhausted, it returns io.EOF. If the caller re-uses the same
	// instance in multiple calls to Next, it should call msg.Reset() in between
	// each call.
	Next(msg proto.Message) error
}

type jsonRequestParser struct {
	dec         *json.Decoder
	unmarshaler jsonpb.Unmarshaler
}

// NewJSONRequestParser returns a RequestParser that reads data in JSON format
// from the given reader.
func NewJSONRequestParser(in io.Reader, resolver jsonpb.AnyResolver) RequestParser {
	return &jsonRequestParser{
		dec:         json.NewDecoder(in),
		unmarshaler: jsonpb.Unmarshaler{AnyResolver: resolver},
	}
}

func (f *jsonRequestParser) Next(m proto.Message) error {
	var msg json.RawMessage
	if err := f.dec.Decode(&msg); err != nil {
		return err
	}

	return f.unmarshaler.Unmarshal(bytes.NewReader(msg), m)
}

// Formatter translates messages into string representations.
type Formatter func(proto.Message) (string, error)

// NewJSONFormatter returns a formatter that returns JSON strings.
func NewJSONFormatter(emitDefaults bool, resolver jsonpb.AnyResolver) Formatter {
	marshaler := jsonpb.Marshaler{
		EmitDefaults: emitDefaults,
		Indent:       "  ",
		AnyResolver:  resolver,
	}
	return marshaler.MarshalToString
}

func anyResolver(source DescriptorSource) (jsonpb.AnyResolver, error) {
	files, _ := GetAllFiles(source)

	var er dynamic.ExtensionRegistry
	for _, fd := range files {
		er.AddExtensionsFromFile(fd)
	}
	mf := dynamic.NewMessageFactoryWithExtensionRegistry(&er)
	return dynamic.AnyResolver(mf, files...), nil
}

// RequestParserAndFormatterFor returns a request parser and formatter for the given format.
func RequestParserAndFormatterFor(descSource DescriptorSource, emitJSONDefaultFields bool, in io.Reader) (RequestParser, Formatter, error) {
	resolver, err := anyResolver(descSource)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating message resolver: %v", err)
	}

	return NewJSONRequestParser(in, resolver), NewJSONFormatter(emitJSONDefaultFields, resolver), nil
}

func RequestParserFor(descSource DescriptorSource, in io.Reader) (RequestParser, error) {
	resolver, err := anyResolver(descSource)
	if err != nil {
		return nil, fmt.Errorf("error creating message resolver: %v", err)
	}

	return NewJSONRequestParser(in, resolver), nil
}

func ParseFormatterByDesc(descSource DescriptorSource, emitFields bool) (Formatter, error) {
	resolver, err := anyResolver(descSource)
	return NewJSONFormatter(emitFields, resolver), err
}

type EventHandler struct {
	descSource DescriptorSource
	formatter  func(proto.Message) (string, error)
}

var DefaultEventHandler *EventHandler

func SetDefaultEventHandler(descSource DescriptorSource, formatter Formatter) *EventHandler {
	en := &EventHandler{
		descSource: descSource,
		formatter:  formatter,
	}

	DefaultEventHandler = en
	return en
}

func (h *EventHandler) FormatResponse(resp proto.Message) string {
	if respStr, err := h.formatter(resp); err != nil {
		return ""

	} else {
		return respStr
	}
}
