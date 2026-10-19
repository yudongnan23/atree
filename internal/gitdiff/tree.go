package gitdiff

import (
	"sort"
	"strings"
)

// Node is a file or directory in the repo tree.
type Node struct {
	Name     string
	Path     string // repo-relative; "" for the root
	Dir      bool
	Status   Status // files only
	Changes  int    // changed files at or below this node
	Children []*Node
}

// BuildTree arranges files into a directory tree: directories before files,
// each sorted by name, with change counts aggregated upwards.
func BuildTree(files []File) *Node {
	root := &Node{Dir: true}
	dirs := map[string]*Node{"": root}
	var dirFor func(path string) *Node
	dirFor = func(path string) *Node {
		if n, ok := dirs[path]; ok {
			return n
		}
		parent, name := "", path
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			parent, name = path[:i], path[i+1:]
		}
		n := &Node{Name: name, Path: path, Dir: true}
		p := dirFor(parent)
		p.Children = append(p.Children, n)
		dirs[path] = n
		return n
	}
	for _, f := range files {
		parent, name := "", f.Path
		if i := strings.LastIndexByte(f.Path, '/'); i >= 0 {
			parent, name = f.Path[:i], f.Path[i+1:]
		}
		d := dirFor(parent)
		d.Children = append(d.Children, &Node{Name: name, Path: f.Path, Status: f.Status})
		if f.Status != Unchanged {
			for p := parent; ; {
				dirs[p].Changes++
				if p == "" {
					break
				}
				if i := strings.LastIndexByte(p, '/'); i >= 0 {
					p = p[:i]
				} else {
					p = ""
				}
			}
		}
	}
	for _, d := range dirs {
		sort.Slice(d.Children, func(i, j int) bool {
			a, b := d.Children[i], d.Children[j]
			if a.Dir != b.Dir {
				return a.Dir
			}
			return a.Name < b.Name
		})
	}
	return root
}
