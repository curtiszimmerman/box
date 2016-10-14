package builder

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/docker/engine-api/types"
	mruby "github.com/mitchellh/go-mruby"
)

func (b *Builder) commit(cacheKey string, hook func(b *Builder, id string) (string, error)) error {
	if os.Getenv("NO_CACHE") != "" {
		cacheKey = ""
	}

	id, err := b.createEmptyContainer()
	if err != nil {
		return err
	}

	signals := make(chan os.Signal)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		_, ok := <-signals
		if ok {
			b.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{Force: true})
		}
	}()

	defer func() {
		b.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{Force: true})
		signal.Reset(syscall.SIGINT, syscall.SIGTERM)
	}()

	if hook != nil {
		tmp, err := hook(b, id)
		if err != nil {
			return err
		}

		if tmp != "" && os.Getenv("NO_CACHE") == "" {
			cacheKey = tmp
		}
	}

	b.resetConfig()

	commitResp, err := b.client.ContainerCommit(context.Background(), id, types.ContainerCommitOptions{Config: b.config, Comment: cacheKey})
	if err != nil {
		return fmt.Errorf("Error during commit: %v", err)
	}

	// try a clean remove first, otherwise the defer above will take over in a last-ditch attempt
	err = b.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{})
	if err != nil {
		return fmt.Errorf("Could not remove intermediate container %q: %v", id, err)
	}

	b.config.Image = commitResp.ID

	return nil
}

func createException(m *mruby.Mrb, msg string) mruby.Value {
	val, err := m.Class("Exception", nil).New(mruby.String(msg))
	if err != nil {
		panic(fmt.Sprintf("could not construct exception for return: %v", err))
	}

	return val
}

func (b *Builder) resetConfig() {
	b.config.WorkingDir = b.workdir
	b.config.User = b.user
	b.config.Cmd = b.cmd
	b.config.Entrypoint = b.entrypoint
}

func extractStringArgs(m *mruby.Mrb) []string {
	args := m.GetArgs()
	strArgs := []string{}
	for _, arg := range args {
		if arg.Type() != mruby.TypeProc {
			strArgs = append(strArgs, arg.String())
		}
	}

	return strArgs
}

func (b *Builder) consultCache(cacheKey string) (bool, error) {
	if os.Getenv("NO_CACHE") == "" {
		if b.config.Image != "" {
			images, err := b.client.ImageList(context.Background(), types.ImageListOptions{All: true})
			if err != nil {
				return false, err
			}

			for _, img := range images {
				if img.ParentID == b.config.Image {
					inspect, _, err := b.client.ImageInspectWithRaw(context.Background(), img.ID)
					if err != nil {
						return false, err
					}

					if inspect.Comment == cacheKey {
						fmt.Printf("+++ Cache hit: using %q\n", img.ID)
						b.config = inspect.Config
						b.user = b.config.User
						b.workdir = b.config.WorkingDir
						b.cmd = b.config.Cmd
						b.entrypoint = b.config.Entrypoint
						b.config.Image = img.ID

						return true, nil
					}
				}
			}
		}
	}

	return false, nil
}

func tarPath(rel, target string) (string, error) {
	fi, err := os.Lstat(rel)
	if err != nil {
		return "", err
	}

	f, err := ioutil.TempFile("", "box-copy.")
	if err != nil {
		return "", err
	}

	tw := tar.NewWriter(f)

	if fi.IsDir() {
		err := filepath.Walk(rel, func(path string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			fmt.Printf("--- Copy: %s -> %s\n", path, filepath.Join(target, path))

			header, err := tar.FileInfoHeader(fi, filepath.Join(target, path))
			if err != nil {
				return err
			}

			header.Linkname = filepath.Join(target, path)
			header.Name = filepath.Join(target, path)

			if err := tw.WriteHeader(header); err != nil {
				return err
			}

			p, err := os.Open(path)
			if err != nil {
				return err
			}

			if header.Typeflag == tar.TypeReg {
				_, err = io.Copy(tw, p)
				if err != nil && err != io.EOF {
					p.Close()
					return err
				}

				p.Close()
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	} else if !fi.IsDir() {
		header, err := tar.FileInfoHeader(fi, target)
		if err != nil {
			return "", err
		}

		header.Name = target
		header.Linkname = target

		if err := tw.WriteHeader(header); err != nil {
			return "", err
		}

		p, err := os.Open(rel)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(tw, p)
		if err != nil && err != io.EOF {
			p.Close()
			return "", err
		}
		p.Close()
	}

	tw.Close()
	f.Close()

	return f.Name(), nil
}

func sumFile(fn string) (string, error) {
	f, err := os.Open(fn)
	if err != nil {
		return "", err
	}

	hash := sha512.New512_256()
	_, err = io.Copy(hash, f)
	if err != nil && err != io.EOF {
		f.Close()
		return "", err
	}
	cacheKey := fmt.Sprintf("box:copy %s", hex.EncodeToString(hash.Sum(nil)))
	f.Close()

	return cacheKey, nil
}

func runHook(b *Builder, id string) (string, error) {
	cearesp, err := b.client.ContainerAttach(context.Background(), id, types.ContainerAttachOptions{Stream: true, Stdout: true, Stderr: true})
	if err != nil {
		return "", fmt.Errorf("Could not attach to container: %v", err)
	}

	err = b.client.ContainerStart(context.Background(), id, types.ContainerStartOptions{})
	if err != nil {
		return "", fmt.Errorf("Could not start container: %v", err)
	}

	fmt.Println("------ BEGIN OUTPUT ------")

	_, err = io.Copy(os.Stdout, cearesp.Reader)
	if err != nil && err != io.EOF {
		return "", err
	}

	fmt.Println("------ END OUTPUT ------")

	stat, err := b.client.ContainerWait(context.Background(), id)
	if err != nil {
		return "", err
	}

	if stat != 0 {
		return "", fmt.Errorf("Command exited with status %d for container %q", stat, id)
	}

	return "", nil
}

func printPull(reader io.Reader) error {
	idmap := map[string][]string{}
	idlist := []string{}

	fmt.Println()

	buf := bufio.NewReader(reader)
	for {
		line, err := buf.ReadBytes('\n')
		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		var unpacked map[string]interface{}
		if err := json.Unmarshal(line, &unpacked); err != nil {
			return err
		}

		progress, ok := unpacked["progress"].(string)
		if !ok {
			progress = ""
		}

		status := unpacked["status"].(string)
		id, ok := unpacked["id"].(string)
		if !ok {
			fmt.Printf("\x1b[%dA", len(idmap)+1)
			fmt.Printf("\r\x1b[K%s\n", status)
		} else {
			fmt.Printf("\x1b[%dA", len(idmap))
			if _, ok := idmap[id]; !ok {
				idlist = append(idlist, id)
			}

			idmap[id] = []string{status, progress}
		}

		for _, id := range idlist {
			fmt.Printf("\r\x1b[K%s %s %s\n", id, idmap[id][0], idmap[id][1])
		}
	}

	return nil
}

func (b *Builder) createEmptyContainer() (string, error) {
	cont, err := b.client.ContainerCreate(
		context.Background(),
		b.config,
		nil,
		nil,
		"",
	)

	return cont.ID, err
}

func iterateRubyHash(arg *mruby.MrbValue, fn func(*mruby.MrbValue, *mruby.MrbValue) error) error {
	hash := arg.Hash()

	// mruby does not expose native maps, just ruby primitives, so we have to
	// iterate through it with indexing functions instead of typical idioms.
	keys, err := hash.Keys()
	if err != nil {
		return err
	}

	for i := 0; i < keys.Array().Len(); i++ {
		key, err := keys.Array().Get(i)
		if err != nil {
			return err
		}

		value, err := hash.Get(key)
		if err != nil {
			return err
		}

		if err := fn(key, value); err != nil {
			return err
		}
	}

	return nil
}

func (b *Builder) containerContent(fn string) ([]byte, error) {
	id, err := b.createEmptyContainer()
	if err != nil {
		return nil, err
	}

	defer b.client.ContainerRemove(context.Background(), id, types.ContainerRemoveOptions{Force: true})

	rc, _, err := b.client.CopyFromContainer(context.Background(), id, fn)
	if err != nil {
		return nil, err
	}

	tr := tar.NewReader(rc)
	defer rc.Close()

	var header *tar.Header

	for {
		header, err = tr.Next()
		if err == io.EOF {
			break
		}

		if err != nil {
			return nil, err
		}

		if header.Name == filepath.Base(fn) {
			break
		}
	}

	if header == nil || header.Name != filepath.Base(fn) {
		return nil, fmt.Errorf("Could not find %q in container", fn)
	}

	return ioutil.ReadAll(tr)
}
