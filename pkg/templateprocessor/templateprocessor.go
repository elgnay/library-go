// Copyright Contributors to the Open Cluster Management project

package templateprocessor

import (
	"bytes"
	goerr "errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/Masterminds/sprig/v3"
	"github.com/ghodss/yaml"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/klog"
)

const KubernetesYamlsDelimiter = "(?m)^-{3}$"
const KubernetesYamlsDelimiterString = "---\n"

//TemplateProcessor this structure holds all objects for the TemplateProcessor
type TemplateProcessor struct {
	//reader a TemplateReader to read the data source
	reader TemplateReader
	//template
	tmpl *template.Template
	//Options to configure the TemplateProcessor
	options *Options
}

//TemplateReader defines the needed functions
type TemplateReader interface {
	//Retreive an asset from the data source
	Asset(templatePath string) ([]byte, error)
	//List all available assets in the data source
	AssetNames() ([]string, error)
	//Transform the assets into a JSON. This is used to transform the asset into an unstructrued.Unstructured object.
	//For example: if the asset is a yaml, you can use yaml.YAMLToJSON(b []byte) as implementation as it is shown in
	//testread_test.go
	ToJSON(b []byte) ([]byte, error)
}

//Options defines for the available options for the templateProcessor
type Options struct {
	KindsOrder      SortType
	DelimiterString string
	Delimiter       string
	//Override the default order, it contains the kind order which the templateProcess must use to sort all resources.
	CreateUpdateKindsOrder KindsOrder
	DeleteKindsOrder       KindsOrder
	MissingKeyType         MissingKeyType
}

type SortType string

type MissingKeyType string

const (
	sortTypeCreateUpdate SortType = "create-update"
	sortTypeDelete       SortType = "delete"
)

const (
	MissingKeyTypeZero    MissingKeyType = "missingkey=zero"
	MissingKeyTypeError   MissingKeyType = "missingkey=error"
	MissingKeyTypeInvalid MissingKeyType = "missingkey=invalid"
	MissingKeyTypeDefault MissingKeyType = "missingkey=default"
)

type KindsOrder []string

//defaultKindsOrder the default order
var defaultCreateUpdateKindsOrder KindsOrder = []string{
	"Namespace",
	"NetworkPolicy",
	"ResourceQuota",
	"LimitRange",
	"PodSecurityPolicy",
	"PodDisruptionBudget",
	"ServiceAccount",
	"Secret",
	"SecretList",
	"ConfigMap",
	"StorageClass",
	"PersistentVolume",
	"PersistentVolumeClaim",
	"CustomResourceDefinition",
	"ClusterRole",
	"ClusterRoleList",
	"ClusterRoleBinding",
	"ClusterRoleBindingList",
	"Role",
	"RoleList",
	"RoleBinding",
	"RoleBindingList",
	"Service",
	"DaemonSet",
	"Pod",
	"ReplicationController",
	"ReplicaSet",
	"Deployment",
	"HorizontalPodAutoscaler",
	"StatefulSet",
	"Job",
	"CronJob",
	"Ingress",
	"APIService",
}

var defaultDeleteKindsOrder KindsOrder = []string{
	"APIService",
	"Ingress",
	"Service",
	"CronJob",
	"Job",
	"StatefulSet",
	"HorizontalPodAutoscaler",
	"Deployment",
	"ReplicaSet",
	"ReplicationController",
	"Pod",
	"DaemonSet",
	"RoleBindingList",
	"RoleBinding",
	"RoleList",
	"Role",
	"ClusterRoleBindingList",
	"ClusterRoleBinding",
	"ClusterRoleList",
	"ClusterRole",
	"CustomResourceDefinition",
	"PersistentVolumeClaim",
	"PersistentVolume",
	"StorageClass",
	"ConfigMap",
	"SecretList",
	"Secret",
	"ServiceAccount",
	"PodDisruptionBudget",
	"PodSecurityPolicy",
	"LimitRange",
	"ResourceQuota",
	"NetworkPolicy",
	"Namespace",
}

//NewTemplateProcessor creates a new applier
//reader: The TemplateReader to use to read the templates
//options: The possible options for the templateprocessor
func NewTemplateProcessor(
	reader TemplateReader,
	options *Options,
) (*TemplateProcessor, error) {
	if reader == nil {
		return nil, goerr.New("reader is nil")
	}
	if options == nil {
		options = &Options{}
	}
	if options.CreateUpdateKindsOrder == nil {
		options.CreateUpdateKindsOrder = defaultCreateUpdateKindsOrder
	}
	if options.DeleteKindsOrder == nil {
		options.DeleteKindsOrder = defaultDeleteKindsOrder
	}
	if options.KindsOrder == "" {
		options.KindsOrder = sortTypeCreateUpdate
	}
	if options.Delimiter == "" {
		options.Delimiter = KubernetesYamlsDelimiter
	}
	if options.DelimiterString == "" {
		options.DelimiterString = KubernetesYamlsDelimiterString
	}
	if options.MissingKeyType == "" {
		options.MissingKeyType = MissingKeyTypeZero
	}
	re, err := regexp.Compile(options.Delimiter)
	if err != nil {
		return nil, err
	}
	ss := re.FindAllString(options.DelimiterString, -1)
	if len(ss) != 1 || ss[0] != strings.TrimSuffix(options.DelimiterString, "\n") {
		return nil,
			fmt.Errorf("Regexp options.Delimiter %s is not perfectly matching options.DelimiterString %s",
				options.Delimiter,
				options.DelimiterString)
	}
	tmpl := template.New("yamls").
		Option(string(options.MissingKeyType)).
		Funcs(ApplierFuncMap())
	tmpl = tmpl.Funcs(TemplateFuncMap(tmpl)).
		Funcs(sprig.TxtFuncMap())
	return &TemplateProcessor{
		reader:  reader,
		tmpl:    tmpl,
		options: options,
	}, nil
}

func (tp *TemplateProcessor) SetDeleteOrder() {
	tp.options.KindsOrder = sortTypeDelete
}

func (tp *TemplateProcessor) SetCreateUpdateOrder() {
	tp.options.KindsOrder = sortTypeCreateUpdate
}

//TemplateResources render the given templates with the provided values
//The resources are not sorted and returned in the same template provided order
func (tp *TemplateProcessor) TemplateResources(
	templateNames []string,
	values interface{},
) ([][]byte, error) {
	results := make([][]byte, 0)
	for _, templateName := range templateNames {
		result, err := tp.TemplateResource(templateName, values)
		if err != nil {
			return nil, err
		}
		if result != nil {
			results = append(results, result)
		}
	}
	return results, nil
}

//TemplateResource render the given template with the provided values
func (tp *TemplateProcessor) TemplateResource(
	templateName string,
	values interface{},
) ([]byte, error) {
	klog.V(5).Infof("templateName: %s", templateName)
	if filepath.Base(templateName) == "_helpers.tpl" {
		return nil, nil
	}
	h, _ := tp.reader.Asset(filepath.Join(filepath.Dir(templateName), "_helpers.tpl"))
	b, err := tp.reader.Asset(templateName)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("\nb--->\n%s\n---", string(b))
	h = append(h, b[:]...)
	klog.V(5).Infof("\nh+b--->\n%s\n---", string(h))
	templated, err := tp.TemplateBytes(h, values)
	return templated, err
}

//TemplateBytes render the given template with the provided values
func (tp *TemplateProcessor) TemplateBytes(
	b []byte,
	values interface{},
) ([]byte, error) {
	var buf bytes.Buffer
	tmpl, err := tp.tmpl.Parse(string(b))
	if err != nil {
		return nil, err
	}

	err = tmpl.Execute(&buf, values)
	if err != nil {
		return nil, err
	}

	klog.V(5).Infof("templated:\n%s\n---", buf.String())
	trim := strings.TrimSuffix(buf.String(), "\n")
	trim = strings.TrimSpace(trim)
	if len(trim) == 0 {
		return nil, nil
	}
	return buf.Bytes(), err
}

// TemplateAssetsInPathYaml returns all assets in a path using the provided config.
// The resources are sorted following the order defined in variable kindsOrder
func (tp *TemplateProcessor) TemplateResourcesInPathYaml(
	path string,
	excluded []string,
	recursive bool,
	values interface{},
) ([][]byte, error) {
	us, err := tp.TemplateResourcesInPathUnstructured(path, excluded, recursive, values)
	if err != nil {
		return nil, err
	}

	return ToYAMLsUnstructured(us)
}

func ToYAMLsUnstructured(us []*unstructured.Unstructured) ([][]byte, error) {
	results := make([][]byte, len(us))

	for i, u := range us {
		y, err := ToYAMLUnstructured(u)
		if err != nil {
			return nil, err
		}
		results[i] = y
	}
	return results, nil
}

func ToYAMLUnstructured(u *unstructured.Unstructured) ([]byte, error) {
	j, err := u.MarshalJSON()
	if err != nil {
		return nil, err
	}
	y, err := yaml.JSONToYAML(j)
	if err != nil {
		return nil, err
	}
	return y, nil
}

//AssetNamesInPath returns all asset names with a given path and
// subpath if recursive is set to true, it excludes the assets contained in the excluded parameter
func (tp *TemplateProcessor) AssetNamesInPath(
	path string,
	excluded []string,
	recursive bool,
) ([]string, error) {
	results := make([]string, 0)
	names, err := tp.reader.AssetNames()
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("names: %v", names)
	for _, name := range names {
		if isExcluded(name, excluded) {
			continue
		}
		klog.V(5).Infof("filepath.Dir(%s)=%s", name, filepath.Dir(name))
		if (recursive && strings.HasPrefix(filepath.Join(filepath.Dir(name), name), path)) ||
			(!recursive && filepath.Dir(name) == path) {
			results = append(results, name)
		}
	}
	if len(results) == 0 {
		return results,
			fmt.Errorf("No asset found in path \"%s\" with excluded %v and recursive %t",
				path,
				excluded,
				recursive)
	}
	return results, nil
}

func isExcluded(name string, excluded []string) bool {
	if excluded == nil {
		return false
	}
	for _, e := range excluded {
		if e == name {
			return true
		}
	}
	return false
}

//Assets returns all assets with a given path and
// subpath if recursive set to true, it excludes the assets contained in the excluded parameter
func (tp *TemplateProcessor) Assets(
	path string,
	excluded []string,
	recursive bool,
) (payloads [][]byte, err error) {
	names, err := tp.AssetNamesInPath(path, excluded, recursive)
	if err != nil {
		return nil, err
	}

	for _, name := range names {
		b, err := tp.reader.Asset(name)
		if err != nil {
			return nil, err
		}
		payloads = append(payloads, b)
	}
	return payloads, nil
}

// TemplateResourcesInPathUnstructured returns all assets in a []unstructured.Unstructured and sort them
// The []unstructured.Unstructured are sorted following the order defined in variable kindsOrder
func (tp *TemplateProcessor) TemplateResourcesInPathUnstructured(
	path string,
	excluded []string,
	recursive bool,
	values interface{}) (us []*unstructured.Unstructured, err error) {
	templateNames, err := tp.AssetNamesInPath(path, excluded, recursive)
	if err != nil {
		return nil, err
	}
	klog.V(5).Infof("templateNames: %v", templateNames)
	us, err = tp.TemplateResourcesUnstructured(templateNames, values)
	if err != nil {
		return nil, err
	}
	return us, nil
}

// TemplateResourcesUnstructured returns all assets in a []unstructured.Unstructured and sort them
// The []unstructured.Unstructured are sorted following the order defined in variable kindsOrder
func (tp *TemplateProcessor) TemplateResourcesUnstructured(
	templateNames []string,
	values interface{}) (us []*unstructured.Unstructured, err error) {
	templatedAssets, err := tp.TemplateResources(templateNames, values)
	if err != nil {
		return nil, err
	}
	us, err = tp.BytesArrayToUnstructured(templatedAssets)
	if err != nil {
		return nil, err
	}
	tp.sortUnstructuredForApply(us)
	for _, u := range us {
		klog.V(5).Infof("TemplateResourcesUnstructured sorted u:%s/%s", u.GetKind(), u.GetName())
	}
	return us, nil
}

//BytesArrayToUnstructured transform a [][]byte to an []*unstructured.Unstructured using the TemplateProcessor reader
func (tp *TemplateProcessor) BytesArrayToUnstructured(assets [][]byte) (us []*unstructured.Unstructured, err error) {
	us = make([]*unstructured.Unstructured, 0)
	for _, b := range assets {
		// Maybe the asset contains multiple assets separated by "---\n"
		bb := ConvertStringToArrayOfBytes(string(b), tp.options.Delimiter)
		for _, b := range bb {
			u, err := tp.BytesToUnstructured(b)
			if err != nil {
				return nil, err
			}
			us = append(us, u)
		}
	}
	return us, nil
}

//BytesToUnstructured transform a []byte to an *unstructured.Unstructured using the TemplateProcessor reader
func (tp *TemplateProcessor) BytesToUnstructured(asset []byte) (*unstructured.Unstructured, error) {
	klog.V(5).Infof("assets:\n%s", string(asset))
	j, err := tp.reader.ToJSON(asset)
	if err != nil {
		return nil, err
	}
	u := &unstructured.Unstructured{}
	_, _, err = unstructured.UnstructuredJSONScheme.Decode(j, nil, u)
	klog.V(5).Infof("runtime.IsMissingKind(err):%v\nu:\n%v", runtime.IsMissingKind(err), u)
	if err != nil {
		klog.V(5).Infof("Error: %s", err)
		//In case it is not a kube yaml
		if !runtime.IsMissingKind(err) {
			return nil, err
		}
	}
	return u, nil
}

//sortUnstructuredForApply sorts a list on unstructured
func (tp *TemplateProcessor) sortUnstructuredForApply(us []*unstructured.Unstructured) {
	sort.Slice(us[:], func(i, j int) bool {
		return tp.less(us[i], us[j])
	})
}

func (tp *TemplateProcessor) less(u1, u2 *unstructured.Unstructured) bool {
	if tp.weight(u1) == tp.weight(u2) {
		if u1.GetNamespace() == u2.GetNamespace() {
			return u1.GetName() < u2.GetName()
		}
		return u1.GetNamespace() < u2.GetNamespace()
	}
	return tp.weight(u1) < tp.weight(u2)
}

func (tp *TemplateProcessor) weight(u *unstructured.Unstructured) int {
	kind := u.GetKind()
	var order KindsOrder
	var defaultWeight int
	switch tp.options.KindsOrder {
	case sortTypeCreateUpdate:
		order = tp.options.CreateUpdateKindsOrder
		defaultWeight = len(tp.options.CreateUpdateKindsOrder)
	case sortTypeDelete:
		order = tp.options.DeleteKindsOrder
		defaultWeight = -1
	}
	for i, k := range order {
		if k == kind {
			return i
		}
	}
	return defaultWeight
}

func ConvertArrayOfBytesToString(in [][]byte) (out string) {
	ss := ConvertArrayOfBytesToArrayOfString(in)
	out = fmt.Sprint(strings.Join(ss, KubernetesYamlsDelimiterString))
	return out
}

func ConvertArrayOfBytesToArrayOfString(in [][]byte) (out []string) {
	out = make([]string, 0)
	for _, o := range in {
		out = append(out, string(o))
	}
	return out
}

func ConvertStringToArrayOfBytes(in, delimiter string) (out [][]byte) {
	re := regexp.MustCompile(delimiter)
	ss := re.Split(in, -1)
	out = make([][]byte, 0)
	for _, s := range ss {
		trim := strings.TrimSuffix(s, "\n")
		if len(strings.TrimSpace(trim)) != 0 {
			out = append(out, []byte(s))
		}
	}
	return out
}
