package build

import (
	"fmt"
	"io"

	"github.com/docker/distribution/reference"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/backend"
	"github.com/docker/docker/builder"
	"github.com/docker/docker/builder/dockerfile"
	"github.com/docker/docker/image"
	"github.com/docker/docker/pkg/idtools"
	"github.com/docker/docker/pkg/progress"
	"github.com/docker/docker/pkg/streamformatter"
	"github.com/docker/docker/pkg/stringid"
	"github.com/docker/docker/registry"
	"github.com/pkg/errors"
	"golang.org/x/net/context"
)

// ImageComponent provides an interface for working with images
type ImageComponent interface {
	SquashImage(from string, to string) (string, error)
	TagImageWithReference(image.ID, reference.Named) error
	PushImage(ctx context.Context, image, tag string, metaHeaders map[string][]string, authConfig *types.AuthConfig, output progress.Output) error
}

// Backend provides build functionality to the API router
type Backend struct {
	manager         *dockerfile.BuildManager
	imageComponent  ImageComponent
	registryService registry.Service
}

// NewBackend creates a new build backend from components
func NewBackend(components ImageComponent, builderBackend builder.Backend, idMappings *idtools.IDMappings, registryService registry.Service) *Backend {
	manager := dockerfile.NewBuildManager(builderBackend, idMappings)
	return &Backend{imageComponent: components, manager: manager, registryService: registryService}
}

// Build builds an image from a Source
func (b *Backend) Build(ctx context.Context, config backend.BuildConfig) (string, error) {
	options := config.Options
	tags := options.Tags
	if options.PushAs != "" {
		tags = append(tags, options.PushAs)
	}
	tagger, err := NewTagger(b.imageComponent, config.ProgressWriter.StdoutFormatter, tags)
	if err != nil {
		return "", err
	}

	build, err := b.manager.Build(ctx, config)
	if err != nil {
		return "", err
	}

	var imageID = build.ImageID
	if options.Squash {
		if imageID, err = squashBuild(build, b.imageComponent); err != nil {
			return "", err
		}
	}

	stdout := config.ProgressWriter.StdoutFormatter
	fmt.Fprintf(stdout, "Successfully built %s\n", stringid.TruncateID(imageID))
	err = tagger.TagImages(image.ID(imageID))

	if options.PushAs != "" {
		err = b.pushImage(ctx, options.PushAs, options.AuthConfigs, stdout)
		return imageID, err
	}
	return imageID, err
}

func (b *Backend) pushImage(ctx context.Context, pushAs string, authConfigs map[string]types.AuthConfig, output io.Writer) error {
	ref, err := reference.ParseNormalizedNamed(pushAs)
	if err != nil {
		return err
	}
	repoInfo, err := b.registryService.ResolveRepository(ref)
	if err != nil {
		return err
	}
	authConfig := registry.ResolveAuthConfig(authConfigs, repoInfo.Index)
	return b.imageComponent.PushImage(ctx, ref.String(), "", map[string][]string{}, &authConfig, streamformatter.NewProgressOutput(output))
}

func squashBuild(build *builder.Result, imageComponent ImageComponent) (string, error) {
	var fromID string
	if build.FromImage != nil {
		fromID = build.FromImage.ImageID()
	}
	imageID, err := imageComponent.SquashImage(build.ImageID, fromID)
	if err != nil {
		return "", errors.Wrap(err, "error squashing image")
	}
	return imageID, nil
}
