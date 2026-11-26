package handlers

import (
	"fmt"
	"strings"
)

// successorImage is where Test0rMaik/KleverNodeHub publishes the dashboard image.
const successorImage = "ghcr.io/test0rmaik/klever-node-hub"

// imageForRelease returns the image to run for a release. Releases of the new
// repository are published to GHCR: an official Docker Hub image moves there;
// any other image (a mirror, a local build) only gets the new tag.
func imageForRelease(current, tag string) (string, error) {
	if isSuccessorTag(tag) {
		repo := current
		if i := strings.LastIndex(repo, "@"); i >= 0 {
			repo = repo[:i]
		}
		lastSlash := strings.LastIndex(repo, "/")
		if i := strings.LastIndex(repo[lastSlash+1:], ":"); i >= 0 {
			repo = repo[:lastSlash+1+i]
		}
		for _, p := range []string{"docker.io/", "index.docker.io/", "registry-1.docker.io/"} {
			repo = strings.TrimPrefix(repo, p)
		}
		repo = strings.TrimPrefix(repo, "library/")
		if repo == "ctjaeger/klever-node-hub" {
			return successorImage + ":" + tag, nil
		}
	}
	return replaceImageTag(current, tag)
}

// replaceImageTag replaces the tag portion of an image reference.
// e.g. "ctjaeger/klever-node-hub:v0.3.40" → "ctjaeger/klever-node-hub:v0.3.42"
//
// Refuses digest-pinned references (`image@sha256:...`) because swapping the
// tag of a pinned digest is meaningless. A naive LastIndex(":") split also
// breaks on registry-with-port references like `registry.local:5000/foo` —
// the tag is the substring after the last `:` that follows the last `/`.
func replaceImageTag(image, newTag string) (string, error) {
	if strings.Contains(image, "@") {
		return "", fmt.Errorf("cannot swap tag on digest-pinned image: %s", image)
	}
	lastSlash := strings.LastIndex(image, "/")
	afterSlash := image[lastSlash+1:]
	if idx := strings.LastIndex(afterSlash, ":"); idx >= 0 {
		return image[:lastSlash+1+idx] + ":" + newTag, nil
	}
	return image + ":" + newTag, nil
}
