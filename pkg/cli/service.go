// +build extended

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
	"sync"

	zotErrors "github.com/anuvu/zot/errors"
	"github.com/dustin/go-humanize"
	jsoniter "github.com/json-iterator/go"
	"github.com/olekukonko/tablewriter"
	"gopkg.in/yaml.v2"
)

type SearchService interface {
	getImages(ctx context.Context, config searchConfig, username, password string,
		imageName string) (*imageListStructGQL, error)
	getImagesByDigest(ctx context.Context, config searchConfig, username, password string,
		digest string) (*imageListStructForDigestGQL, error)
	getCveByImage(ctx context.Context, config searchConfig, username, password,
		imageName string) (*cveResult, error)
	getImagesByCveID(ctx context.Context, config searchConfig, username, password string,
		digest string) (*imagesForCveGQL, error)
	getTagsForCVE(ctx context.Context, config searchConfig, username, password, imageName,
		cveID string, getFixed bool) (*tagsForCVE, error)
}

type searchService struct{}

func NewSearchService() SearchService {
	return searchService{}
}

func (service searchService) getImages(ctx context.Context, config searchConfig, username, password string,
	imageName string) (*imageListStructGQL, error) {
	query := fmt.Sprintf(`{ImageList(imageName: "%s") {`+`
									Name Tag Digest ConfigDigest Size Layers {Size Digest}}
							  }`,
		imageName)
	result := &imageListStructGQL{}

	err := service.makeGraphQLQuery(config, username, password, query, result)

	if err != nil {
		if isContextDone(ctx) {
			return nil, nil
		}

		return nil, err
	}

	if result.Errors != nil {
		var errBuilder strings.Builder

		for _, err := range result.Errors {
			fmt.Fprintln(&errBuilder, err.Message)
		}

		if isContextDone(ctx) {
			return nil, nil
		}

		//nolint: goerr113
		return nil, errors.New(errBuilder.String())
	}

	return result, nil
}

func (service searchService) getImagesByDigest(ctx context.Context, config searchConfig, username, password string,
	digest string) (*imageListStructForDigestGQL, error) {
	query := fmt.Sprintf(`{ImageListForDigest(digest: "%s") {`+`
									Name Tag Digest ConfigDigest Size Layers {Size Digest}}
							  }`,
		digest)
	result := &imageListStructForDigestGQL{}

	err := service.makeGraphQLQuery(config, username, password, query, result)

	if err != nil {
		if isContextDone(ctx) {
			return nil, nil
		}

		return nil, err
	}

	if result.Errors != nil && len(result.Errors) > 0 {
		var errBuilder strings.Builder

		for _, err := range result.Errors {
			fmt.Fprintln(&errBuilder, err.Message)
		}

		if isContextDone(ctx) {
			return nil, nil
		}

		//nolint: goerr113
		return nil, errors.New(errBuilder.String())
	}

	return result, nil
}

func (service searchService) getImagesByCveID(ctx context.Context, config searchConfig, username,
	password, cveID string) (*imagesForCveGQL, error) {
	query := fmt.Sprintf(`{ImageListForCVE(id: "%s") {`+`
								Name Tag Digest Size}
						  }`,
		cveID)
	result := &imagesForCveGQL{}

	err := service.makeGraphQLQuery(config, username, password, query, result)

	if err != nil {
		if isContextDone(ctx) {
			return nil, nil
		}

		return nil, err
	}

	if result.Errors != nil {
		var errBuilder strings.Builder

		for _, err := range result.Errors {
			fmt.Fprintln(&errBuilder, err.Message)
		}

		if isContextDone(ctx) {
			return nil, nil
		}

		//nolint: goerr113
		return nil, errors.New(errBuilder.String())
	}

	return result, nil
}

func (service searchService) getCveByImage(ctx context.Context, config searchConfig, username, password,
	imageName string) (*cveResult, error) {
	query := fmt.Sprintf(`{ CVEListForImage (image:"%s")`+
		` { Tag CVEList { Id Title Severity Description `+
		`PackageList {Name InstalledVersion FixedVersion}} } }`, imageName)
	result := &cveResult{}

	err := service.makeGraphQLQuery(config, username, password, query, result)

	if err != nil {
		if isContextDone(ctx) {
			return nil, nil
		}

		return nil, err
	}

	if result.Errors != nil {
		var errBuilder strings.Builder

		for _, err := range result.Errors {
			fmt.Fprintln(&errBuilder, err.Message)
		}

		if isContextDone(ctx) {
			return nil, nil
		}

		//nolint: goerr113
		return nil, errors.New(errBuilder.String())
	}

	result.Data.CVEListForImage.CVEList = groupCVEsBySeverity(result.Data.CVEListForImage.CVEList)

	return result, nil
}

func groupCVEsBySeverity(cveList []cve) []cve {
	high := make([]cve, 0)
	med := make([]cve, 0)
	low := make([]cve, 0)

	for _, cve := range cveList {
		switch cve.Severity {
		case "LOW":
			low = append(low, cve)

		case "MEDIUM":
			med = append(med, cve)

		case "HIGH":
			high = append(high, cve)
		}
	}

	return append(append(high, med...), low...)
}

func isContextDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

func (service searchService) getTagsForCVE(ctx context.Context, config searchConfig,
	username, password, imageName, cveID string, getFixed bool) (*tagsForCVE, error) {
	query := fmt.Sprintf(`{TagListForCve(id: "%s", image: "%s", getFixed: %t) {`+`
								Name Tag Digest Size}
						  }`,
		cveID, imageName, getFixed)
	result := &tagsForCVE{}

	err := service.makeGraphQLQuery(config, username, password, query, result)

	if err != nil {
		if isContextDone(ctx) {
			return nil, nil
		}

		return nil, err
	}

	if result.Errors != nil {
		var errBuilder strings.Builder

		for _, error := range result.Errors {
			fmt.Fprintln(&errBuilder, error.Message)
		}

		if isContextDone(ctx) {
			return nil, nil
		}

		//nolint: goerr113
		return nil, errors.New(errBuilder.String())
	}

	return result, err
}

// Query using JQL, the query string is passed as a parameter
// errors are returned in the stringResult channel, the unmarshalled payload is in resultPtr.
func (service searchService) makeGraphQLQuery(config searchConfig, username, password, query string,
	resultPtr interface{}) error {
	endPoint, err := combineServerAndEndpointURL(*config.servURL, "/query")
	if err != nil {
		return err
	}

	err = makeGraphQLRequest(endPoint, query, username, password, *config.verifyTLS, resultPtr)
	if err != nil {
		return err
	}

	return nil
}

//nolint
func addManifestCallToPool(ctx context.Context, config searchConfig, p *requestsPool, username, password, imageName,
	tagName string, c chan stringResult, wg *sync.WaitGroup) {
	defer wg.Done()

	resultManifest := manifestResponse{}

	manifestEndpoint, err := combineServerAndEndpointURL(*config.servURL,
		fmt.Sprintf("/v2/%s/manifests/%s", imageName, tagName))
	if err != nil {
		if isContextDone(ctx) {
			return
		}
		c <- stringResult{"", err}
	}

	job := manifestJob{
		url:          manifestEndpoint,
		username:     username,
		imageName:    imageName,
		password:     password,
		tagName:      tagName,
		manifestResp: resultManifest,
		config:       config,
	}

	wg.Add(1)
	p.submitJob(&job)
}

type cveResult struct {
	Errors []errorGraphQL `json:"errors"`
	Data   cveData        `json:"data"`
}
type errorGraphQL struct {
	Message string   `json:"message"`
	Path    []string `json:"path"`
}
type packageList struct {
	Name             string `json:"Name"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
}
type cve struct {
	ID          string        `json:"Id"`
	Severity    string        `json:"Severity"`
	Title       string        `json:"Title"`
	Description string        `json:"Description"`
	PackageList []packageList `json:"PackageList"`
}
type cveListForImage struct {
	Tag     string `json:"Tag"`
	CVEList []cve  `json:"CVEList"`
}
type cveData struct {
	CVEListForImage cveListForImage `json:"CVEListForImage"`
}

//nolint: goconst
func (cve cveResult) string(format string) (string, error) {
	switch strings.ToLower(format) {
	case "", defaultOutoutFormat:
		return cve.stringPlainText()
	case "json":
		return cve.stringJSON()
	case "yml", "yaml":
		return cve.stringYAML()
	default:
		return "", ErrInvalidOutputFormat
	}
}

func (cve cveResult) stringPlainText() (string, error) {
	var builder strings.Builder

	table := getCVETableWriter(&builder)

	for _, c := range cve.Data.CVEListForImage.CVEList {
		id := ellipsize(c.ID, cveIDWidth, ellipsis)
		title := ellipsize(c.Title, cveTitleWidth, ellipsis)
		severity := ellipsize(c.Severity, cveSeverityWidth, ellipsis)
		row := make([]string, 3)
		row[colCVEIDIndex] = id
		row[colCVESeverityIndex] = severity
		row[colCVETitleIndex] = title

		table.Append(row)
	}

	table.Render()

	return builder.String(), nil
}

func (cve cveResult) stringJSON() (string, error) {
	var json = jsoniter.ConfigCompatibleWithStandardLibrary
	body, err := json.MarshalIndent(cve.Data.CVEListForImage, "", "  ")

	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (cve cveResult) stringYAML() (string, error) {
	body, err := yaml.Marshal(&cve.Data.CVEListForImage)

	if err != nil {
		return "", err
	}

	return string(body), nil
}

type tagsForCVE struct {
	Errors []errorGraphQL `json:"errors"`
	Data   struct {
		TagListForCve []imageStructGQL `json:"TagListForCve"`
	} `json:"data"`
}

type imagesForCveGQL struct {
	Errors []errorGraphQL `json:"errors"`
	Data   struct {
		ImageListForCVE []imageStructGQL `json:"ImageListForCVE"`
	} `json:"data"`
}

//nolint
type imageStruct struct {
	Name    string `json:"name"`
	Tags    []tags `json:"tags"`
	verbose bool
}

type imageStructGQL struct {
	Name         string     `json:"name"`
	Tag          string     `json:"tag"`
	ConfigDigest string     `json:"configDigest"`
	Digest       string     `json:"digest"`
	Layers       []layerGQL `json:"layers"`
	Size         string     `json:"size"`
	verbose      bool
}

type imageListStructGQL struct {
	Errors []errorGraphQL `json:"errors"`
	Data   struct {
		ImageList []imageStructGQL `json:"ImageList"`
	} `json:"data"`
}

type imageListStructForDigestGQL struct {
	Errors []errorGraphQL `json:"errors"`
	Data   struct {
		ImageList []imageStructGQL `json:"ImageListForDigest"`
	} `json:"data"`
}

//nolint
type tags struct {
	Name         string  `json:"name"`
	Size         uint64  `json:"size"`
	Digest       string  `json:"digest"`
	ConfigDigest string  `json:"configDigest"`
	Layers       []layer `json:"layerDigests"`
}

//nolint
type layer struct {
	Size   uint64 `json:"size"`
	Digest string `json:"digest"`
}

type layerGQL struct {
	Size   string `json:"size"`
	Digest string `json:"digest"`
}

func (img imageStruct) string(format string) (string, error) {
	switch strings.ToLower(format) {
	case "", defaultOutoutFormat:
		return img.stringPlainText()
	case "json":
		return img.stringJSON()
	case "yml", "yaml":
		return img.stringYAML()
	default:
		return "", ErrInvalidOutputFormat
	}
}

func (img imageStructGQL) string(format string) (string, error) {
	switch strings.ToLower(format) {
	case "", defaultOutoutFormat:
		return img.stringPlainText()
	case "json":
		return img.stringJSON()
	case "yml", "yaml":
		return img.stringYAML()
	default:
		return "", ErrInvalidOutputFormat
	}
}

func (img imageStruct) stringPlainText() (string, error) {
	var builder strings.Builder

	table := getImageTableWriter(&builder)
	table.SetColMinWidth(colImageNameIndex, imageNameWidth)
	table.SetColMinWidth(colTagIndex, tagWidth)
	table.SetColMinWidth(colDigestIndex, digestWidth)
	table.SetColMinWidth(colSizeIndex, sizeWidth)

	if img.verbose {
		table.SetColMinWidth(colConfigIndex, configWidth)
		table.SetColMinWidth(colLayersIndex, layersWidth)
	}

	for _, tag := range img.Tags {
		imageName := ellipsize(img.Name, imageNameWidth, ellipsis)
		tagName := ellipsize(tag.Name, tagWidth, ellipsis)
		digest := ellipsize(tag.Digest, digestWidth, "")
		size := ellipsize(strings.ReplaceAll(humanize.Bytes(tag.Size), " ", ""), sizeWidth, ellipsis)
		config := ellipsize(tag.ConfigDigest, configWidth, "")
		row := make([]string, 6)

		row[colImageNameIndex] = imageName
		row[colTagIndex] = tagName
		row[colDigestIndex] = digest
		row[colSizeIndex] = size

		if img.verbose {
			row[colConfigIndex] = config
			row[colLayersIndex] = ""
		}

		table.Append(row)

		if img.verbose {
			for _, entry := range tag.Layers {
				layerSize := ellipsize(strings.ReplaceAll(humanize.Bytes(entry.Size), " ", ""), sizeWidth, ellipsis)
				layerDigest := ellipsize(entry.Digest, digestWidth, "")

				layerRow := make([]string, 6)
				layerRow[colImageNameIndex] = ""
				layerRow[colTagIndex] = ""
				layerRow[colDigestIndex] = ""
				layerRow[colSizeIndex] = layerSize
				layerRow[colConfigIndex] = ""
				layerRow[colLayersIndex] = layerDigest

				table.Append(layerRow)
			}
		}
	}

	table.Render()

	return builder.String(), nil
}

func (img imageStruct) stringJSON() (string, error) {
	var json = jsoniter.ConfigCompatibleWithStandardLibrary
	body, err := json.MarshalIndent(img, "", "  ")

	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (img imageStruct) stringYAML() (string, error) {
	body, err := yaml.Marshal(&img)

	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (img imageStructGQL) stringPlainText() (string, error) {
	var builder strings.Builder

	table := getImageTableWriter(&builder)
	table.SetColMinWidth(colImageNameIndex, imageNameWidth)
	table.SetColMinWidth(colTagIndex, tagWidth)
	table.SetColMinWidth(colDigestIndex, digestWidth)
	table.SetColMinWidth(colSizeIndex, sizeWidth)

	if img.verbose {
		table.SetColMinWidth(colConfigIndex, configWidth)
		table.SetColMinWidth(colLayersIndex, layersWidth)
	}

	imageName := ellipsize(img.Name, imageNameWidth, ellipsis)
	tagName := ellipsize(img.Tag, tagWidth, ellipsis)
	digest := ellipsize(img.Digest, digestWidth, "")
	imgSize, _ := strconv.ParseUint(img.Size, 10, 64)
	size := ellipsize(strings.ReplaceAll(humanize.Bytes(imgSize), " ", ""), sizeWidth, ellipsis)
	config := ellipsize(img.ConfigDigest, configWidth, "")
	row := make([]string, 6)

	row[colImageNameIndex] = imageName
	row[colTagIndex] = tagName
	row[colDigestIndex] = digest
	row[colSizeIndex] = size

	if img.verbose {
		row[colConfigIndex] = config
		row[colLayersIndex] = ""
	}

	table.Append(row)

	if img.verbose {
		for _, entry := range img.Layers {
			layerSize, _ := strconv.ParseUint(entry.Size, 10, 64)
			size := ellipsize(strings.ReplaceAll(humanize.Bytes(layerSize), " ", ""), sizeWidth, ellipsis)
			layerDigest := ellipsize(entry.Digest, digestWidth, "")

			layerRow := make([]string, 6)
			layerRow[colImageNameIndex] = ""
			layerRow[colTagIndex] = ""
			layerRow[colDigestIndex] = ""
			layerRow[colSizeIndex] = size
			layerRow[colConfigIndex] = ""
			layerRow[colLayersIndex] = layerDigest

			table.Append(layerRow)
		}
	}

	table.Render()

	return builder.String(), nil
}

func (img imageStructGQL) stringJSON() (string, error) {
	var json = jsoniter.ConfigCompatibleWithStandardLibrary
	body, err := json.MarshalIndent(img, "", "  ")

	if err != nil {
		return "", err
	}

	return string(body), nil
}

func (img imageStructGQL) stringYAML() (string, error) {
	body, err := yaml.Marshal(&img)

	if err != nil {
		return "", err
	}

	return string(body), nil
}

//nolint
type catalogResponse struct {
	Repositories []string `json:"repositories"`
}

//nolint
type manifestResponse struct {
	Layers []struct {
		MediaType string `json:"mediaType"`
		Digest    string `json:"digest"`
		Size      uint64 `json:"size"`
	} `json:"layers"`
	Annotations struct {
		WsTychoStackerStackerYaml string `json:"ws.tycho.stacker.stacker_yaml"`
		WsTychoStackerGitVersion  string `json:"ws.tycho.stacker.git_version"`
	} `json:"annotations"`
	Config struct {
		Size      int    `json:"size"`
		Digest    string `json:"digest"`
		MediaType string `json:"mediaType"`
	} `json:"config"`
	SchemaVersion int `json:"schemaVersion"`
}

func combineServerAndEndpointURL(serverURL, endPoint string) (string, error) {
	if !isURL(serverURL) {
		return "", zotErrors.ErrInvalidURL
	}

	newURL, err := url.Parse(serverURL)

	if err != nil {
		return "", zotErrors.ErrInvalidURL
	}

	newURL, _ = newURL.Parse(endPoint)

	return newURL.String(), nil
}

func ellipsize(text string, max int, trailing string) string {
	text = strings.TrimSpace(text)
	if len(text) <= max {
		return text
	}

	chopLength := len(trailing)

	return text[:max-chopLength] + trailing
}

func getImageTableWriter(writer io.Writer) *tablewriter.Table {
	table := tablewriter.NewWriter(writer)

	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("  ")
	table.SetNoWhiteSpace(true)

	return table
}

func getCVETableWriter(writer io.Writer) *tablewriter.Table {
	table := tablewriter.NewWriter(writer)

	table.SetAutoWrapText(false)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("")
	table.SetColumnSeparator("")
	table.SetRowSeparator("")
	table.SetHeaderLine(false)
	table.SetBorder(false)
	table.SetTablePadding("  ")
	table.SetNoWhiteSpace(true)
	table.SetColMinWidth(colCVEIDIndex, cveIDWidth)
	table.SetColMinWidth(colCVESeverityIndex, cveSeverityWidth)
	table.SetColMinWidth(colCVETitleIndex, cveTitleWidth)

	return table
}

const (
	imageNameWidth = 32
	tagWidth       = 24
	digestWidth    = 8
	sizeWidth      = 8
	configWidth    = 8
	layersWidth    = 8
	ellipsis       = "..."

	colImageNameIndex = 0
	colTagIndex       = 1
	colDigestIndex    = 2
	colConfigIndex    = 3
	colLayersIndex    = 4
	colSizeIndex      = 5

	cveIDWidth       = 16
	cveSeverityWidth = 8
	cveTitleWidth    = 48

	colCVEIDIndex       = 0
	colCVESeverityIndex = 1
	colCVETitleIndex    = 2

	defaultOutoutFormat = "text"
)
