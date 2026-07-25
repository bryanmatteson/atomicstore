package store

import (
	"fmt"
	"path"
	"strings"
)

// Location represents a storage location
type Location struct {
	Scheme string
	Bucket string
	Path   string
}

// NewLocation creates a new Location
func NewLocation(scheme, bucket, path string) Location {
	return Location{
		Scheme: scheme,
		Bucket: bucket,
		Path:   path,
	}
}

// ParseLocation parses a URI into a Location
func ParseLocation(uri string) (Location, error) {
	parsedURI, err := ParseURI(uri)
	if err != nil {
		return Location{}, err
	}

	return Location{
		Scheme: parsedURI.Scheme,
		Bucket: parsedURI.Bucket,
		Path:   parsedURI.Key,
	}, nil
}

// String returns the string representation of the location
func (l Location) String() string {
	return FormatLocationURI(l.Scheme, l.Bucket, l.Path)
}

// WithKey returns a new Location with the given key appended to the path
func (l Location) WithKey(key string) Location {
	return Location{
		Scheme: l.Scheme,
		Bucket: l.Bucket,
		Path:   path.Join(l.Path, key),
	}
}

// WithSubpath returns a new Location with the given subpath appended to the path
func (l Location) WithSubpath(subpath string) Location {
	return Location{
		Scheme: l.Scheme,
		Bucket: l.Bucket,
		Path:   path.Join(l.Path, subpath),
	}
}

// WithPrefix returns a new Location with the given prefix prepended to the path
func (l Location) WithPrefix(prefix string) Location {
	// If we already have a prefix, combine them
	if l.Path != "" {
		return Location{
			Scheme: l.Scheme,
			Bucket: l.Bucket,
			Path:   path.Join(prefix, l.Path),
		}
	}

	return Location{
		Scheme: l.Scheme,
		Bucket: l.Bucket,
		Path:   prefix,
	}
}

// Parent returns the parent location (up one directory)
func (l Location) Parent() Location {
	if l.Path == "" || l.Path == "/" {
		return l
	}

	parentPath := path.Dir(l.Path)
	if parentPath == "." {
		parentPath = ""
	}

	return Location{
		Scheme: l.Scheme,
		Bucket: l.Bucket,
		Path:   parentPath,
	}
}

// IsParentOf returns true if this location is a parent of the other location
func (l Location) IsParentOf(other Location) bool {
	if l.Scheme != other.Scheme || l.Bucket != other.Bucket {
		return false
	}

	if l.Path == "" {
		return true // Root is parent of everything in same bucket
	}

	return strings.HasPrefix(other.Path, l.Path+"/")
}

// RelativePath returns the relative path from this location to another
func (l Location) RelativePath(other Location) (string, error) {
	if l.Scheme != other.Scheme || l.Bucket != other.Bucket {
		return "", fmt.Errorf("locations are in different buckets or schemes")
	}

	if l.Path == "" {
		return other.Path, nil
	}

	if !strings.HasPrefix(other.Path, l.Path) {
		return "", fmt.Errorf("other location is not a child of this location")
	}

	relPath := strings.TrimPrefix(other.Path, l.Path)
	relPath = strings.TrimPrefix(relPath, "/")

	return relPath, nil
}
