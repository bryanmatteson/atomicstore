package codec

import (
	"fmt"
	"strings"
	"sync"
)

// Registry manages available codec implementations
type Registry struct {
	codecs         map[string]Constructor
	contentTypes   map[string]string
	fileExtensions map[string]string
	defaultConfigs map[string]Config
	mu             sync.RWMutex
}

// Constructor creates a new Codec instance
type Constructor func() Codec

// DefaultRegistry is the shared instance
var DefaultRegistry = NewRegistry()

// NewRegistry creates a new codec registry
func NewRegistry() *Registry {
	return &Registry{
		codecs:         make(map[string]Constructor),
		contentTypes:   make(map[string]string),
		fileExtensions: make(map[string]string),
		defaultConfigs: make(map[string]Config),
	}
}

// Register adds a codec to the registry
func (r *Registry) Register(name string, constructor Constructor, contentTypes []string, fileExts []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lowerName := strings.ToLower(name)
	r.codecs[lowerName] = constructor

	for _, ct := range contentTypes {
		r.contentTypes[strings.ToLower(ct)] = lowerName
	}

	for _, ext := range fileExts {
		r.fileExtensions[strings.ToLower(ext)] = lowerName
	}
}

// New creates a new codec instance by name
func (r *Registry) New(name string, opts ...Option) (Codec, error) {
	r.mu.RLock()
	constructor, ok := r.codecs[strings.ToLower(name)]
	defaultConfig := r.defaultConfigs[strings.ToLower(name)]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("codec not registered: %s", name)
	}

	codec := constructor()

	if defaultConfig != nil {
		if err := defaultConfig.ApplyTo(codec); err != nil {
			return nil, err
		}
	}

	// Apply user configs
	for _, opt := range opts {
		if err := opt(codec); err != nil {
			return nil, err
		}
	}

	return codec, nil
}

// ForContentType creates a codec for the given content type
func (r *Registry) ForContentType(contentType string, opts ...Option) (Codec, error) {
	mainType := strings.Split(strings.ToLower(contentType), ";")[0]
	mainType = strings.TrimSpace(mainType)

	r.mu.RLock()
	codecName, ok := r.contentTypes[mainType]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no codec registered for content type: %s", contentType)
	}

	return r.New(codecName, opts...)
}

// ForFileExtension creates a codec for the given file extension
func (r *Registry) ForFileExtension(ext string, opts ...Option) (Codec, error) {
	ext = strings.ToLower(strings.TrimPrefix(ext, "."))

	r.mu.RLock()
	codecName, ok := r.fileExtensions[ext]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("no codec registered for file extension: %s", ext)
	}

	return r.New(codecName, opts...)
}

// SetDefaultConfig sets default configurations for a codec type
func (r *Registry) SetDefaultConfig(name string, config Config) {
	r.mu.Lock()
	defer r.mu.Unlock()

	lowerName := strings.ToLower(name)
	r.defaultConfigs[lowerName] = config
}

// Get returns a codec by name from the default registry
func Get(name string, opts ...Option) (Codec, error) {
	return DefaultRegistry.New(name, opts...)
}

// ForContentType gets a codec by content type from the default registry
func ForContentType(contentType string, opts ...Option) (Codec, error) {
	return DefaultRegistry.ForContentType(contentType, opts...)
}

// ForFileExtension gets a codec by file extension from the default registry
func ForFileExtension(ext string, opts ...Option) (Codec, error) {
	return DefaultRegistry.ForFileExtension(ext, opts...)
}

// ListRegisteredCodecs returns a list of all registered codec names
func (r *Registry) ListRegisteredCodecs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.codecs))
	for name := range r.codecs {
		names = append(names, name)
	}
	return names
}

// ListRegisteredCodecs returns a list of all registered codec names
func ListRegisteredCodecs() []string {
	return DefaultRegistry.ListRegisteredCodecs()
}
