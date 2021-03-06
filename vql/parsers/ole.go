package parsers

import (
	"archive/zip"
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"

	"www.velocidex.com/golang/oleparse"
	"www.velocidex.com/golang/velociraptor/glob"
	vql_subsystem "www.velocidex.com/golang/velociraptor/vql"
	vfilter "www.velocidex.com/golang/vfilter"
)

type _OLEVBAArgs struct {
	Filenames []string `vfilter:"required,field=file"`
	Accessor  string   `vfilter:"optional,field=accessor"`
	MaxSize   int64    `vfilter:"optional,field=max_size,doc=Maximum size of file we load into memory."`
}

type _OLEVBAPlugin struct{}

func _OLEVBAPlugin_ParseFile(
	ctx context.Context,
	filename string,
	scope *vfilter.Scope,
	arg *_OLEVBAArgs) ([]*oleparse.VBAModule, error) {
	accessor := glob.GetAccessor(arg.Accessor, ctx)
	fd, err := accessor.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	stat, err := fd.Stat()
	if err != nil {
		return nil, err
	}

	// Its a directory - not really an error just skip it.
	if stat.IsDir() {
		return nil, nil
	}

	signature := make([]byte, len(oleparse.OLE_SIGNATURE))
	_, err = io.ReadAtLeast(fd, signature, len(oleparse.OLE_SIGNATURE))
	if err != nil {
		return nil, err
	}

	if string(signature) == oleparse.OLE_SIGNATURE {
		fd.Seek(0, os.SEEK_SET)
		data, err := ioutil.ReadAll(fd)
		if err != nil {
			return nil, err
		}
		return oleparse.ParseBuffer(data)
	}

	// Maybe it is a zip file.
	reader, ok := fd.(io.ReaderAt)
	if !ok {
		return nil, errors.New("file is not seekable")
	}

	zfd, err := zip.NewReader(reader, stat.Size())
	if err == nil {
		for _, f := range zfd.File {
			if oleparse.BINFILE_NAME.MatchString(f.Name) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				data, err := ioutil.ReadAll(rc)
				if err != nil {
					return nil, err
				}
				return oleparse.ParseBuffer(data)
			}
		}

	}

	return nil, errors.New("Not an OLE file.")
}

func (self _OLEVBAPlugin) Call(
	ctx context.Context,
	scope *vfilter.Scope,
	args *vfilter.Dict) <-chan vfilter.Row {
	output_chan := make(chan vfilter.Row)

	go func() {
		defer close(output_chan)

		arg := &_OLEVBAArgs{}
		err := vfilter.ExtractArgs(scope, args, arg)
		if err != nil {
			scope.Log("olevba: %s", err.Error())
			return
		}

		for _, filename := range arg.Filenames {
			macros, err := _OLEVBAPlugin_ParseFile(ctx, filename, scope, arg)
			if err != nil {
				scope.Log("olevba: while parsing %v:  %s", filename, err)
				continue
			}

			for _, macro_info := range macros {
				output_chan <- vql_subsystem.RowToDict(scope, macro_info).Set(
					"filename", filename)
			}
		}
	}()

	return output_chan
}

func (self _OLEVBAPlugin) Info(scope *vfilter.Scope,
	type_map *vfilter.TypeMap) *vfilter.PluginInfo {
	return &vfilter.PluginInfo{
		Name:    "olevba",
		Doc:     "Extracts VBA Macros from Office documents.",
		ArgType: type_map.AddType(scope, &_OLEVBAArgs{}),
	}
}

func init() {
	vql_subsystem.RegisterPlugin(&_OLEVBAPlugin{})
}
