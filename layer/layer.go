package layer

import (
	"os"
	"path/filepath"
)

const dirMode = 0777

// Layer represents a filesystem layer in a container build.
type Layer struct {
	dirname    string
	workingDir string
}

// New creates a new layer.
func New(dirname string, workingDir string) (*Layer, error) {
	var err error

	if workingDir == "" {
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}

	if _, err = filepath.Rel(workingDir, dirname); err != nil {
		return nil, err
	}

	if workingDir, err = filepath.Abs(dirname); err != nil {
		return nil, err
	}

	return &Layer{
		dirname:    dirname,
		workingDir: workingDir,
	}, nil
}

func (l *Layer) inChdir(fun func(l *Layer) error) error {
	wd, err := os.Getwd()
	if err != nil {
		return err
	}

	if err := os.Chdir(l.workingDir); err != nil {
		return err
	}

	if err := fun(l); err != nil {
		return err
	}

	return os.Chdir(wd)
}

// Create creates the layer
func (l *Layer) Create() error {
	return l.inChdir(func(l *Layer) error { return os.Mkdir(l.dirname, dirMode) })
}

// Remove removes the layer
func (l *Layer) Remove() error {
	return l.inChdir(func(l *Layer) error { return os.RemoveAll(l.dirname) })
}
