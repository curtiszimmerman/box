package layer

import (
	"io/ioutil"
	"os"
	"strings"
	. "testing"

	. "gopkg.in/check.v1"
)

type layerSuite struct{}

var _ = Suite(&layerSuite{})

func TestLayer(t *T) {
	TestingT(t)
}

func inTmpDir(c *C, fun func(c *C)) {
	wd, err := os.Getwd()
	c.Assert(err, IsNil)

	name, err := ioutil.TempDir("", "box-layer-test")
	c.Assert(err, IsNil)

	c.Assert(os.Chdir(name), IsNil)

	fun(c)

	defer os.Chdir(wd)
	defer os.Remove(name)
}

func (s *layerSuite) TestNew(c *C) {
	table := []struct {
		pathargs   [2]string
		errCheck   Checker
		layerCheck Checker
		errStr     string
	}{
		{
			[2]string{"..", ""},
			NotNil,
			IsNil,
			"can't make .. relative",
		},
		{
			[2]string{"..", ".."},
			IsNil,
			NotNil,
			"",
		},
		{
			[2]string{".", ".."},
			NotNil,
			IsNil,
			"",
		},
		{
			[2]string{".", ""},
			NotNil,
			IsNil,
			"",
		},
		{
			[2]string{"", ""},
			NotNil,
			IsNil,
			"",
		},
	}

	for i, check := range table {
		comment := Commentf("Index: %d", i)
		l, err := New(check.pathargs[0], check.pathargs[1])
		c.Assert(err, check.errCheck, comment)
		c.Assert(l, check.layerCheck, comment)
		if check.errStr != "" {
			c.Assert(strings.Contains(err.Error(), check.errStr), Equals, true, comment)
		}
	}

	dir, err := ioutil.TempDir("", "box-layer-test")
	c.Assert(err, IsNil)
	defer os.RemoveAll(dir)

	l, err := New(dir, "")
	c.Assert(err, IsNil)
	c.Assert(l, NotNil)
}
