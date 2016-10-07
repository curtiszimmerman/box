package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/docker/engine-api/types"
	mruby "github.com/mitchellh/go-mruby"
)

// Definition is a jump table definition used for programming the DSL into the
// mruby interpreter.
type Definition struct {
	Func    Func
	ArgSpec mruby.ArgSpec
}

var jumpTable = map[string]Definition{
	"from":       {from, mruby.ArgsReq(1)},
	"run":        {run, mruby.ArgsAny()},
	"user":       {user, mruby.ArgsBlock() | mruby.ArgsReq(1)},
	"workdir":    {workdir, mruby.ArgsBlock() | mruby.ArgsReq(1)},
	"env":        {env, mruby.ArgsAny()},
	"cmd":        {cmd, mruby.ArgsAny()},
	"entrypoint": {entrypoint, mruby.ArgsAny()},
}

// Func is a builder DSL function used to interact with docker.
type Func func(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value)

func entrypoint(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	stringArgs := []string{}
	for _, arg := range m.GetArgs() {
		stringArgs = append(stringArgs, arg.String())
	}

	b.config.Entrypoint = stringArgs
	var err error

	b.id, err = b.commit()
	if err != nil {
		return mruby.String(fmt.Sprintf("Error creating intermediate container: %v", err)), nil
	}

	return nil, nil
}

func from(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	args := m.GetArgs()

	b.config.Image = args[0].String()
	b.config.Tty = true
	b.config.AttachStdout = true
	b.config.AttachStderr = true

	var err error
	b.id, err = b.commit()
	if err != nil {
		return mruby.String(err.Error()), nil
	}

	return mruby.String(fmt.Sprintf("Response: %v", b.id)), nil
}

func run(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	if b.imageID == "" {
		return mruby.String("`from` must be the first docker command`"), nil
	}

	stringArgs := []string{}
	for _, arg := range m.GetArgs() {
		stringArgs = append(stringArgs, arg.String())
	}

	cmd := b.config.Cmd

	b.config.Cmd = append([]string{"/bin/sh", "-c"}, stringArgs...)
	defer func() { b.config.Cmd = cmd }()

	var err error

	b.id, err = b.commit()
	if err != nil {
		return mruby.String(fmt.Sprintf("Error creating intermediate container: %v", err)), nil
	}

	cearesp, err := b.client.ContainerAttach(context.Background(), b.id, types.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		return mruby.String(fmt.Sprintf("Error attaching to execution context %q: %v", b.id, err)), nil
	}

	err = b.client.ContainerStart(context.Background(), b.id, types.ContainerStartOptions{})
	if err != nil {
		return mruby.String(fmt.Sprintf("Error attaching to execution context %q: %v", b.id, err)), nil
	}

	_, err = io.Copy(os.Stdout, cearesp.Reader)
	if err != nil && err != io.EOF {
		return mruby.String(err.Error()), nil
	}

	return nil, nil
}

func user(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	args := m.GetArgs()

	b.config.User = args[0].String()
	val, err := m.Yield(args[1], args[0])
	b.config.User = ""
	b.id = ""

	if err != nil {
		return mruby.String(fmt.Sprintf("Could not yield: %v", err)), nil
	}

	return val, nil
}

func workdir(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	args := m.GetArgs()

	b.config.WorkingDir = args[0].String()
	val, err := m.Yield(args[1], args[0])
	b.config.WorkingDir = ""
	b.id = ""

	if err != nil {
		return mruby.String(fmt.Sprintf("Could not yield: %v", err)), nil
	}

	return val, nil
}

func env(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	args := m.GetArgs()
	hash := args[0].Hash()

	// mruby does not expose native maps, just ruby primitives, so we have to
	// iterate through it with indexing functions instead of typical idioms.
	keys, err := hash.Keys()
	if err != nil {
		return mruby.String(err.Error()), nil
	}

	for i := 0; i < keys.Array().Len(); i++ {
		key, err := keys.Array().Get(i)
		if err != nil {
			return mruby.String(err.Error()), nil
		}

		value, err := hash.Get(key)
		if err != nil {
			return mruby.String(err.Error()), nil
		}

		b.config.Env = append(b.config.Env, fmt.Sprintf("%s=%s", key.String(), value.String()))
	}

	b.id, err = b.commit()
	if err != nil {
		return mruby.String(fmt.Sprintf("Error creating intermediate container: %v", err)), nil
	}

	return nil, nil
}

func cmd(b *Builder, m *mruby.Mrb, self *mruby.MrbValue) (mruby.Value, mruby.Value) {
	args := m.GetArgs()

	stringArgs := []string{}
	for _, arg := range args {
		stringArgs = append(stringArgs, arg.String())
	}

	b.config.Cmd = stringArgs

	var err error
	b.id, err = b.commit()
	if err != nil {
		return mruby.String(fmt.Sprintf("Error creating intermediate container: %v", err)), nil
	}

	return nil, nil
}
