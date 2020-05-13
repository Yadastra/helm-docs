package helm

import (
	"bufio"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"regexp"
	"sort"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

var valuesDescriptionRegex = regexp.MustCompile("^\\s*# (.*) -- (.*)$")
var commentContinuationRegex = regexp.MustCompile("^\\s*# (.*)$")
var defaultValueRegex = regexp.MustCompile("^\\s*# @default -- (.*)$")

type ChartMetaMaintainer struct {
	Email string
	Name  string
	Url   string
}

type ChartMeta struct {
	ApiVersion    string `yaml:"apiVersion"`
	Name          string
	Version       string
	KubeVersion   string `yaml:"kubeVersion"`
	Description   string
	Keywords      []string
	Home          string
	Sources       []string
	Maintainers   []ChartMetaMaintainer
	Type          string
	Engine        string
	icon          string
	AppVersion    string `yaml:"appVersion"`
	Deprecated    bool
	tillerVersion string
}

type ChartRequirementsItem struct {
	Name       string
	Version    string
	Repository string
}

type ChartRequirements struct {
	Dependencies []ChartRequirementsItem
}

type ChartValueDescription struct {
	Description string
	Default     string
}

type ChartDocumentationInfo struct {
	ChartMeta
	ChartRequirements

	ChartDirectory          string
	ChartValues             map[interface{}]interface{}
	ChartValuesDescriptions map[string]ChartValueDescription
}

func getYamlFileContents(filename string) ([]byte, error) {
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, err
	}

	yamlFileContents, err := ioutil.ReadFile(filename)

	if err != nil {
		panic(err)
	}

	return []byte(yamlFileContents), nil
}

func yamlLoadAndCheck(yamlFileContents []byte, out interface{}) {
	err := yaml.Unmarshal(yamlFileContents, out)

	if err != nil {
		panic(err)
	}
}

func isErrorInReadingNecessaryFile(filePath string, loadError error) bool {
	if loadError != nil {
		if os.IsNotExist(loadError) {
			log.Printf("Required chart file %s missing. Skipping documentation for chart", filePath)
			return true
		} else {
			log.Printf("Error occurred in reading chart file %s. Skipping documentation for chart", filePath)
			return true
		}
	}

	return false
}

func parseChartFile(chartDirectory string) (ChartMeta, error) {
	chartYamlPath := path.Join(chartDirectory, "Chart.yaml")
	chartMeta := ChartMeta{}
	yamlFileContents, err := getYamlFileContents(chartYamlPath)

	if isErrorInReadingNecessaryFile(chartYamlPath, err) {
		return chartMeta, err
	}

	yamlLoadAndCheck(yamlFileContents, &chartMeta)
	return chartMeta, nil
}

func requirementKey(requirement ChartRequirementsItem) string {
	return fmt.Sprintf("%s/%s", requirement.Repository, requirement.Name)
}

func parseChartRequirementsFile(chartDirectory string, apiVersion string) (ChartRequirements, error) {
	var requirementsPath string

	if apiVersion == "v1" {
		requirementsPath = path.Join(chartDirectory, "requirements.yaml")

		if _, err := os.Stat(requirementsPath); os.IsNotExist(err) {
			return ChartRequirements{Dependencies: []ChartRequirementsItem{}}, nil
		}
	} else {
		requirementsPath = path.Join(chartDirectory, "Chart.yaml")
	}

	chartRequirements := ChartRequirements{}
	yamlFileContents, err := getYamlFileContents(requirementsPath)

	if isErrorInReadingNecessaryFile(requirementsPath, err) {
		return chartRequirements, err
	}

	yamlLoadAndCheck(yamlFileContents, &chartRequirements)

	sort.Slice(chartRequirements.Dependencies[:], func(i, j int) bool {
		return requirementKey(chartRequirements.Dependencies[i]) < requirementKey(chartRequirements.Dependencies[j])
	})

	return chartRequirements, nil
}

func parseChartValuesFile(chartDirectory string) (map[interface{}]interface{}, error) {
	valuesPath := path.Join(chartDirectory, "values.yaml")
	values := make(map[interface{}]interface{})
	yamlFileContents, err := getYamlFileContents(valuesPath)

	if isErrorInReadingNecessaryFile(valuesPath, err) {
		return values, err
	}

	yamlLoadAndCheck(yamlFileContents, &values)
	return values, nil
}

func parseChartValuesFileComments(chartDirectory string) (map[string]ChartValueDescription, error) {
	valuesPath := path.Join(chartDirectory, "values.yaml")
	valuesFile, err := os.Open(valuesPath)

	if isErrorInReadingNecessaryFile(valuesPath, err) {
		return map[string]ChartValueDescription{}, err
	}

	defer valuesFile.Close()

	var description, key string
	keyToDescriptions := make(map[string]ChartValueDescription)
	scanner := bufio.NewScanner(valuesFile)
	foundValuesComment := false

	for scanner.Scan() {
		currentLine := scanner.Text()

		// If we've not yet found a values comment with a key name, try and find one on each line
		if !foundValuesComment {
			match := valuesDescriptionRegex.FindStringSubmatch(currentLine)
			if len(match) < 3 {
				continue
			}

			foundValuesComment = true
			key = match[1]
			description = match[2]
			continue
		}

		// If we've already found a values comment, on the next line try and parse a custom default value. If we find one
		// that completes parsing for this key, add it to the list and reset to searching for a new key
		match := defaultValueRegex.FindStringSubmatch(currentLine)

		if len(match) > 1 {
			keyToDescriptions[key] = ChartValueDescription{
				Description: description,
				Default:     match[1],
			}

			foundValuesComment = false
			continue
		}

		// Otherwise, see if there's a comment continuing the description from the previous line
		match = commentContinuationRegex.FindStringSubmatch(currentLine)
		if len(match) > 1 {
			description = description + " " + match[1]
			continue
		}

		// If we haven't continued by this point, we didn't match any of the comment formats we want, so we need to add
		// the in progress value to the map, and reset to looking for a new key
		keyToDescriptions[key] = ChartValueDescription{
			Description: description,
		}

		foundValuesComment = false
	}

	return keyToDescriptions, nil
}

func ParseChartInformation(chartDirectory string) (ChartDocumentationInfo, error) {
	var chartDocInfo ChartDocumentationInfo
	var err error

	chartDocInfo.ChartDirectory = chartDirectory
	chartDocInfo.ChartMeta, err = parseChartFile(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	chartDocInfo.ChartRequirements, err = parseChartRequirementsFile(chartDirectory, chartDocInfo.ApiVersion)
	if err != nil {
		return chartDocInfo, err
	}

	chartDocInfo.ChartValues, err = parseChartValuesFile(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	chartDocInfo.ChartValuesDescriptions, err = parseChartValuesFileComments(chartDirectory)
	if err != nil {
		return chartDocInfo, err
	}

	return chartDocInfo, nil
}
