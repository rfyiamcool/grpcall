package grpcall

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jhump/protoreflect/grpcreflect"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	reflectpb "google.golang.org/grpc/reflection/grpc_reflection_v1alpha"
)

var (
	callMaxTime = 60 * time.Second

	descSourceController = NewDescSourceEntry()
)

const (
	ProtoSetMode     = iota // 0
	ProtoFilesMode          // 1
	ProtoReflectMode        // 2
)

type DescSourceEntry struct {
	ctx    context.Context
	cancel context.CancelFunc

	descSource   DescriptorSource
	descMode     int
	descLastTime time.Time

	protoset    multiString
	protoFiles  multiString
	importPaths multiString
}

func NewDescSourceEntry() *DescSourceEntry {
	ctx, cancel := context.WithCancel(context.Background())

	desc := &DescSourceEntry{
		ctx:    ctx,
		cancel: cancel,
	}

	return desc
}

func SetProtoSetFiles(fileName string) {
	descSourceController.SetProtoSetFiles(fileName)
}

func SetProtoFiles(importPath string, protoFile string) {
	descSourceController.SetProtoFiles(importPath, protoFile)
}

func SetMode(mode int) {
	descSourceController.SetMode(mode)
}

func GetDescSource() (DescriptorSource, error) {
	return descSourceController.GetDescSource()
}

func GetRemoteDescSource(target string) error {
	// parse proto by grpc reflet api
	return nil
}

func InitDescSource() error {
	return descSourceController.InitDescSource()
}

func AysncNotifyDesc() {
	descSourceController.AysncNotifyDesc()
}

func (d *DescSourceEntry) SetProtoSetFiles(fileName string) {
	d.protoset.Set(fileName)
}

func (d *DescSourceEntry) SetProtoFiles(importPath string, protoFile string) {
	d.importPaths.Set(importPath)
	d.protoFiles.Set(protoFile)
}

func (d *DescSourceEntry) SetMode(mode int) {
	d.descMode = mode
}

func (d *DescSourceEntry) GetDescSource() (DescriptorSource, error) {
	switch descSourceController.descMode {
	case ProtoSetMode:
		return descSourceController.descSource, nil

	case ProtoFilesMode:
		return descSourceController.descSource, nil

	default:
		return descSourceController.descSource, errors.New("only eq ProtoSetMode and ProtoFilesMode")
	}
}

func (d *DescSourceEntry) InitDescSource() error {
	var err error
	var desc DescriptorSource

	switch descSourceController.descMode {
	case ProtoSetMode:
		// parse proto by protoset
		desc, err = DescriptorSourceFromProtoSets(descSourceController.protoset...)
		if err != nil {
			return errors.New("Failed to process proto descriptor sets")
		}

		descSourceController.descSource = desc

	case ProtoFilesMode:
		// parse proto by protoFiles
		descSourceController.descSource, err = DescriptorSourceFromProtoFiles(
			descSourceController.importPaths,
			descSourceController.protoFiles...,
		)
		if err != nil {
			return errors.New("Failed to process proto source files")
		}

		descSourceController.descSource = desc

	default:
		return errors.New("only eq ProtoSetMode and ProtoFilesMode")
	}

	return nil
}

func (d *DescSourceEntry) AysncNotifyDesc() {
	go func() {
		q := make(chan os.Signal, 1)
		signal.Notify(q, syscall.SIGUSR1)

		for {
			select {
			case <-q:
				d.InitDescSource()

			case <-d.ctx.Done():
				return
			}
		}
	}()
}

func (d *DescSourceEntry) Close() {
	d.cancel()
}

type EngineHandler struct {
	// grpc clients
	clients     map[string]*grpc.ClientConn
	clientsLock sync.RWMutex

	eventHandler InvocationEventHandler
	descCtl      *DescSourceEntry
	invokeCtl    *InvokeHandler

	dialTime      time.Duration
	keepAliveTime time.Duration

	ctx    context.Context
	cancel context.CancelFunc
}

type Option func(*EngineHandler) error

func SetDialTime(val time.Duration) Option {
	return func(o *EngineHandler) error {
		o.dialTime = val
		return nil
	}
}

func SetKeepAliveTime(val time.Duration) Option {
	return func(o *EngineHandler) error {
		o.keepAliveTime = val
		return nil
	}
}

func SetCtx(val context.Context) Option {
	return func(o *EngineHandler) error {
		o.ctx = val
		return nil
	}
}

func SetDescSourceCtl(val *DescSourceEntry) Option {
	return func(o *EngineHandler) error {
		o.descCtl = val
		return nil
	}
}

func New(handler InvocationEventHandler, options ...Option) (*EngineHandler, error) {
	e := new(EngineHandler)

	// default values
	e.ctx, e.cancel = context.WithCancel(context.Background())
	e.dialTime = 10 * time.Second
	e.keepAliveTime = 64 * time.Second
	e.eventHandler = handler
	e.descCtl = descSourceController
	e.clients = make(map[string]*grpc.ClientConn, 10)

	for _, opt := range options {
		if opt != nil {
			if err := opt(e); err != nil {
				return nil, err
			}
		}
	}

	return e, nil
}

func (e *EngineHandler) DoConnect(target string) (*grpc.ClientConn, error) {
	e.clientsLock.RLock() // read lock
	if conn, ok := e.clients[target]; ok {
		e.clientsLock.RUnlock()
		return conn, nil
	}

	e.clientsLock.RUnlock()

	ctx, cancel := context.WithTimeout(e.ctx, e.dialTime)
	defer cancel()

	var opts []grpc.DialOption
	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:    e.keepAliveTime,
		Timeout: e.keepAliveTime,
	}))

	cc, err := BlockingDial(ctx, target, opts...)
	if err != nil {
		return cc, err
	}

	e.clientsLock.Lock() // write lock
	defer e.clientsLock.Unlock()

	e.clients[target] = cc
	return cc, err
}

func (e *EngineHandler) Init() error {
	var err error

	err = e.InitFormater()
	if err != nil {
		return err
	}

	return nil
}

func (e *EngineHandler) InitFormater() error {
	formater, err := ParseFormatterByDesc(descSourceController.descSource, true)
	if err != nil {
		return err
	}

	eventHandler := SetDefaultEventHandler(descSourceController.descSource, formater)
	e.invokeCtl = newInvokeHandler(eventHandler)
	return nil
}

func (e *EngineHandler) Close() error {
	e.cancel()

	e.clientsLock.Lock()
	defer e.clientsLock.Unlock()
	for _, cc := range e.clients {
		cc.Close()
	}

	return nil
}

func (e *EngineHandler) Call(target, serviceName, methodName, data string) error {
	return e.invokeCall(nil, target, serviceName, methodName, data)
}

func (e *EngineHandler) CallWithAddr(target, serviceName, methodName, data string) error {
	return e.invokeCall(nil, target, serviceName, methodName, data)
}

func (e *EngineHandler) CallWithClient(client *grpc.ClientConn, serviceName, methodName, data string) error {
	if client != nil {
		return errors.New("invalid grpc client")
	}

	return e.invokeCall(client, "", serviceName, methodName, data)
}

// invokeCall request target grpc server
func (e *EngineHandler) invokeCall(gclient *grpc.ClientConn, target, serviceName, methodName, data string) error {
	if target == "" || serviceName == "" || methodName == "" {
		return errors.New("target or serverName or methodName is null")
	}

	var (
		err       error
		cc        *grpc.ClientConn
		connErr   error
		refClient *grpcreflect.Client

		addlHeaders multiString
		rpcHeaders  multiString
		reflHeaders multiString

		descSource DescriptorSource
	)

	descSource = descSourceController.descSource

	// parse proto by grpc reflet api
	if descSourceController.descMode == ProtoReflectMode {
		md := MetadataFromHeaders(append(addlHeaders, reflHeaders...))
		refCtx := metadata.NewOutgoingContext(e.ctx, md)
		cc, connErr = e.DoConnect(target)
		if connErr != nil {
			return connErr
		}

		refClient = grpcreflect.NewClient(refCtx, reflectpb.NewServerReflectionClient(cc))
		descSource = DescriptorSourceFromServer(e.ctx, refClient)
	}

	if gclient == nil {
		cc, connErr = e.DoConnect(target)
		if connErr != nil {
			return connErr
		}
	} else {
		cc = gclient
	}

	var inData io.Reader
	inData = strings.NewReader(data)

	rf, _, err := RequestParserAndFormatterFor(descSource, false, inData)
	if err != nil {
		return errors.New("request parse and format failed")
	}

	err = e.invokeCtl.InvokeRPC(e.ctx, descSource, cc, serviceName, methodName,
		append(addlHeaders, rpcHeaders...),
		e.eventHandler, rf.Next,
	)

	if err != nil {
		return err
	}

	// if h.Status.Code() != codes.OK {
	return nil
}

type multiString []string

func (s *multiString) String() string {
	return strings.Join(*s, ",")
}

func (s *multiString) IsEmpty() bool {
	if len(*s) > 0 {
		return false
	}

	return true
}

func (s *multiString) Set(value string) error {
	*s = append(*s, value)
	return nil
}
