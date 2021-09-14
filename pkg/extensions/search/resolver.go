package search

//go:generate go run github.com/99designs/gqlgen

import (
	"context"
	"fmt"
	"strings"

	"github.com/anuvu/zot/pkg/log"
	"github.com/aquasecurity/trivy/integration/config"

	cveinfo "github.com/anuvu/zot/pkg/extensions/search/cve"
	digestinfo "github.com/anuvu/zot/pkg/extensions/search/digest"
	"github.com/anuvu/zot/pkg/storage"
) // THIS CODE IS A STARTING POINT ONLY. IT WILL NOT BE UPDATED WITH SCHEMA CHANGES.

// Resolver ...
type Resolver struct {
	cveInfo         *cveinfo.CveInfo
	storeController storage.StoreController
	digestInfo      *digestinfo.DigestInfo
}

// Query ...
func (r *Resolver) Query() QueryResolver {
	return &queryResolver{r}
}

type queryResolver struct{ *Resolver }

type cveDetail struct {
	Title       string
	Description string
	Severity    string
	PackageList []*PackageInfo
}

// GetResolverConfig ...
func GetResolverConfig(log log.Logger, storeController storage.StoreController) Config {
	cveInfo, err := cveinfo.GetCVEInfo(storeController, log)
	if err != nil {
		panic(err)
	}

	digestInfo := digestinfo.NewDigestInfo(storeController, log)
	resConfig := &Resolver{cveInfo: cveInfo, storeController: storeController, digestInfo: digestInfo}

	return Config{Resolvers: resConfig, Directives: DirectiveRoot{},
		Complexity: ComplexityRoot{}}
}

func (r *queryResolver) CVEListForImage(ctx context.Context, image string) (*CVEResultForImage, error) {
	trivyConfig := r.cveInfo.GetTrivyConfig(image)

	r.cveInfo.Log.Info().Str("image", image).Msg("scanning image")

	isValidImage, err := r.cveInfo.IsValidImageFormat(image)
	if !isValidImage {
		r.cveInfo.Log.Debug().Str("image", image).Msg("image media type not supported for scanning")

		return &CVEResultForImage{}, err
	}

	cveResults, err := cveinfo.ScanImage(trivyConfig)
	if err != nil {
		r.cveInfo.Log.Error().Err(err).Msg("unable to scan image repository")

		return &CVEResultForImage{}, err
	}

	var copyImgTag string

	if strings.Contains(image, ":") {
		copyImgTag = strings.Split(image, ":")[1]
	}

	cveidMap := make(map[string]cveDetail)

	for _, result := range cveResults {
		for _, vulnerability := range result.Vulnerabilities {
			pkgName := vulnerability.PkgName

			installedVersion := vulnerability.InstalledVersion

			var fixedVersion string
			if vulnerability.FixedVersion != "" {
				fixedVersion = vulnerability.FixedVersion
			} else {
				fixedVersion = "Not Specified"
			}

			_, ok := cveidMap[vulnerability.VulnerabilityID]
			if ok {
				cveDetailStruct := cveidMap[vulnerability.VulnerabilityID]

				pkgList := cveDetailStruct.PackageList

				pkgList = append(pkgList,
					&PackageInfo{Name: &pkgName, InstalledVersion: &installedVersion, FixedVersion: &fixedVersion})

				cveDetailStruct.PackageList = pkgList

				cveidMap[vulnerability.VulnerabilityID] = cveDetailStruct
			} else {
				newPkgList := make([]*PackageInfo, 0)

				newPkgList = append(newPkgList,
					&PackageInfo{Name: &pkgName, InstalledVersion: &installedVersion, FixedVersion: &fixedVersion})

				cveidMap[vulnerability.VulnerabilityID] = cveDetail{Title: vulnerability.Title,
					Description: vulnerability.Description, Severity: vulnerability.Severity, PackageList: newPkgList}
			}
		}
	}

	cveids := []*Cve{}

	for id, cveDetail := range cveidMap {
		vulID := id

		desc := cveDetail.Description

		title := cveDetail.Title

		severity := cveDetail.Severity

		pkgList := cveDetail.PackageList

		cveids = append(cveids,
			&Cve{ID: &vulID, Title: &title, Description: &desc, Severity: &severity, PackageList: pkgList})
	}

	return &CVEResultForImage{Tag: &copyImgTag, CVEList: cveids}, nil
}

func (r *queryResolver) ImageListForCve(ctx context.Context, id string) ([]*ImgResultForCve, error) {
	finalCveResult := []*ImgResultForCve{}

	r.cveInfo.Log.Info().Msg("extracting repositories")

	defaultStore := r.storeController.DefaultStore

	defaultTrivyConfig := r.cveInfo.CveTrivyController.DefaultCveConfig

	repoList, err := defaultStore.GetRepositories()
	if err != nil {
		r.cveInfo.Log.Error().Err(err).Msg("unable to search repositories")

		return finalCveResult, err
	}

	r.cveInfo.Log.Info().Msg("scanning each global repository")

	cveResult, err := r.getImageListForCVE(repoList, id, defaultStore, defaultTrivyConfig)
	if err != nil {
		r.cveInfo.Log.Error().Err(err).Msg("error getting cve list for global repositories")

		return finalCveResult, err
	}

	finalCveResult = append(finalCveResult, cveResult...)

	subStore := r.storeController.SubStore
	for route, store := range subStore {
		subRepoList, err := store.GetRepositories()
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Msg("unable to search repositories")

			return cveResult, err
		}

		subTrivyConfig := r.cveInfo.CveTrivyController.SubCveConfig[route]

		subCveResult, err := r.getImageListForCVE(subRepoList, id, store, subTrivyConfig)
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Msg("unable to get cve result for sub repositories")

			return finalCveResult, err
		}

		finalCveResult = append(finalCveResult, subCveResult...)
	}

	return finalCveResult, nil
}

func (r *queryResolver) getImageListForCVE(repoList []string, id string, imgStore storage.ImageStore,
	trivyConfig *config.Config) ([]*ImgResultForCve, error) {
	cveResult := []*ImgResultForCve{}

	for _, repo := range repoList {
		r.cveInfo.Log.Info().Str("repo", repo).Msg("extracting list of tags available in image repo")

		name := repo

		tags, err := r.cveInfo.GetImageListForCVE(repo, id, imgStore, trivyConfig)
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Msg("error getting tag")

			return cveResult, err
		}

		if len(tags) != 0 {
			cveResult = append(cveResult, &ImgResultForCve{Name: &name, Tags: tags})
		}
	}

	return cveResult, nil
}

func (r *queryResolver) ImageListWithCVEFixed(ctx context.Context, id string, image string) (*ImgResultForFixedCve, error) { // nolint: lll
	imgResultForFixedCVE := &ImgResultForFixedCve{}

	r.cveInfo.Log.Info().Str("image", image).Msg("retrieving image path")

	imagePath := r.cveInfo.LayoutUtils.GetImageRepoPath(image)

	r.cveInfo.Log.Info().Str("image", image).Msg("retrieving trivy config")

	trivyConfig := r.cveInfo.GetTrivyConfig(image)

	r.cveInfo.Log.Info().Str("image", image).Msg("extracting list of tags available in image")

	tagsInfo, err := r.cveInfo.GetImageTagsWithTimestamp(image)
	if err != nil {
		r.cveInfo.Log.Error().Err(err).Msg("unable to read image tags")

		return imgResultForFixedCVE, err
	}

	infectedTags := make([]cveinfo.TagInfo, 0)

	var hasCVE bool

	for _, tag := range tagsInfo {
		trivyConfig.TrivyConfig.Input = fmt.Sprintf("%s:%s", imagePath, tag.Name)

		isValidImage, _ := r.cveInfo.IsValidImageFormat(fmt.Sprintf("%s:%s", image, tag.Name))
		if !isValidImage {
			r.cveInfo.Log.Debug().Str("image",
				image+":"+tag.Name).Msg("image media type not supported for scanning, adding as an infected image")

			infectedTags = append(infectedTags, cveinfo.TagInfo{Name: tag.Name, Timestamp: tag.Timestamp})

			continue
		}

		r.cveInfo.Log.Info().Str("image", image+":"+tag.Name).Msg("scanning image")

		results, err := cveinfo.ScanImage(trivyConfig)
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Str("image", image+":"+tag.Name).Msg("unable to scan image")

			continue
		}

		hasCVE = false

		for _, result := range results {
			for _, vulnerability := range result.Vulnerabilities {
				if vulnerability.VulnerabilityID == id {
					hasCVE = true

					break
				}
			}
		}

		if hasCVE {
			infectedTags = append(infectedTags, cveinfo.TagInfo{Name: tag.Name, Timestamp: tag.Timestamp})
		}
	}

	var finalTagList []*TagInfo

	if len(infectedTags) != 0 {
		r.cveInfo.Log.Info().Msg("comparing fixed tags timestamp")

		fixedTags := cveinfo.GetFixedTags(tagsInfo, infectedTags)

		finalTagList = getGraphqlCompatibleTags(fixedTags)
	} else {
		r.cveInfo.Log.Info().Str("image", image).Str("cve-id", id).Msg("image does not contain any tag that have given cve")

		finalTagList = getGraphqlCompatibleTags(tagsInfo)
	}

	imgResultForFixedCVE = &ImgResultForFixedCve{Tags: finalTagList}

	return imgResultForFixedCVE, nil
}

func (r *queryResolver) ImageListForDigest(ctx context.Context, id string) ([]*ImgResultForDigest, error) {
	imgResultForDigest := []*ImgResultForDigest{}

	r.digestInfo.Log.Info().Msg("extracting repositories")

	defaultStore := r.storeController.DefaultStore

	repoList, err := defaultStore.GetRepositories()
	if err != nil {
		r.digestInfo.Log.Error().Err(err).Msg("unable to search repositories")

		return imgResultForDigest, err
	}

	r.digestInfo.Log.Info().Msg("scanning each global repository")

	partialImgResultForDigest, err := r.getImageListForDigest(repoList, id)
	if err != nil {
		r.cveInfo.Log.Error().Err(err).Msg("unable to get image and tag list for global repositories")

		return imgResultForDigest, err
	}

	imgResultForDigest = append(imgResultForDigest, partialImgResultForDigest...)

	subStore := r.storeController.SubStore
	for _, store := range subStore {
		subRepoList, err := store.GetRepositories()
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Msg("unable to search sub-repositories")

			return imgResultForDigest, err
		}

		partialImgResultForDigest, err = r.getImageListForDigest(subRepoList, id)
		if err != nil {
			r.cveInfo.Log.Error().Err(err).Msg("unable to get image and tag list for sub-repositories")

			return imgResultForDigest, err
		}

		imgResultForDigest = append(imgResultForDigest, partialImgResultForDigest...)
	}

	return imgResultForDigest, nil
}

func (r *queryResolver) getImageListForDigest(repoList []string, digest string) ([]*ImgResultForDigest, error) {
	imgResultForDigest := []*ImgResultForDigest{}

	var errResult error

	for _, repo := range repoList {
		r.digestInfo.Log.Info().Str("repo", repo).Msg("filtering list of tags in image repo by digest")

		tags, err := r.digestInfo.GetImageTagsByDigest(repo, digest)
		if err != nil {
			r.digestInfo.Log.Error().Err(err).Msg("unable to get filtered list of image tags")
			errResult = err

			continue
		}

		if len(tags) != 0 {
			name := repo

			imgResultForDigest = append(imgResultForDigest, &ImgResultForDigest{Name: &name, Tags: tags})
		}
	}

	return imgResultForDigest, errResult
}

func getGraphqlCompatibleTags(fixedTags []cveinfo.TagInfo) []*TagInfo {
	finalTagList := make([]*TagInfo, 0)

	for _, tag := range fixedTags {
		copyTag := tag.Name

		copyTimeStamp := tag.Timestamp

		finalTagList = append(finalTagList, &TagInfo{Name: &copyTag, Timestamp: &copyTimeStamp})
	}

	return finalTagList
}
