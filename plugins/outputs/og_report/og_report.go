package og_report

import (
	"crypto/tls"
	_ "embed"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	og "github.com/amplia-iiot/opengate-go"
	og_http "github.com/amplia-iiot/opengate-go/http_client"
	"github.com/amplia-iiot/opengate-go/logger"
	"github.com/amplia-iiot/opengate-go/odm_model"
	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/plugins/outputs"
)

type OGReport struct {
	ApiKey                 string          `toml:"apikey"`
	Username               string          `toml:"username"`
	CollectUrl             string          `toml:"collecturl"`
	DeviceInPathTagName    string          `toml:"deviceinpath"` //Nombre del tag donde viene el deviceId del path. deviceInPath y deviceInBody. Si deviceInBody esta y no deviceInPath, se setea deviceInPath en lo que haya en deviceInUri o en measurement en ese orden
	DeviceIdBodyTagName    string          `toml:"deviceidbody"` //Nombre del tag donde viene el deviceid del body
	DeviceInUriTagName     string          `toml:"deviceinuri"`  //Nombre del tag donde viene el deviceid de la uri. Si vacio se coge el measurement
	IncludeFieldNotMatched bool            `toml:"allfields"`    //Si esta a true incluira todos los fields que vengan aunque no matcheen, manteniendo el dato como viene
	Timeout                config.Duration `toml:"timeout"`
	ClientRestOption       og_http.ClientOptions
	Log                    telegraf.Logger `toml:"-"`
	Model                  ModelConfig     `toml:"model"`
}
type Filler struct {
	metrics                []telegraf.Metric
	deviceIdInBody         string
	pathInBody             []string
	deviceIdInUri          string
	modelName              string
	includeFieldNotMatched bool
}

type EnumConfig struct {
	CollectValue string `toml:"collectValue"`
	OGValue      string `toml:"ogValue"`
}

type SubRelationConfig struct {
	Field        string `toml:"field"`
	OgDataStream string `toml:"ogDataStream"`
	DataType     string `toml:"dataType,omitempty"`
	Factor       string `toml:"factor,omitempty"`
}
type RelationConfig struct {
	Field        string               `toml:"field"`
	OgDataStream string               `toml:"ogDataStream"`
	Alias        string               `toml:"alias,omitempty"`
	DataType     string               `toml:"dataType,omitempty"`
	Factor       string               `toml:"factor,omitempty"`
	SubRelations []*SubRelationConfig `toml:"subrelations,omitempty"`
	Enums        []*EnumConfig        `toml:"enums,omitempty"`
}
type ModelConfig struct {
	ModelName string            `toml:"modelname"`
	Relations []*RelationConfig `toml:"relations"`
}

func (f *Filler) Fill() (collectInfo []odm_model.CollectIot) {
	noGroupedCollectInfo := []og.CollectInfo{}
	rawFieldsDs := make(map[string][]odm_model.Datapoint) //para poder agrupar los datapoint de un mismo DS de los campos que no hacen match
	for _, metric := range f.metrics {
		for fieldName, fieldValue := range metric.Fields() {
			if relation := og.GetRelation(fieldName, f.modelName); relation == nil {
				if f.includeFieldNotMatched {
					id := fieldName
					rawFieldsDs[id] = append(rawFieldsDs[id], odm_model.Datapoint{Value: fieldValue, At: metric.Time().UnixMilli()})
				}
			} else {
				og_collect_t := og.CollectInfo{ModelName: f.modelName, FieldName: fieldName, Ts: metric.Time().UnixMilli(), FieldValue: fmt.Sprintf("%v", fieldValue)}
				noGroupedCollectInfo = append(noGroupedCollectInfo, og_collect_t)
			}
		}
	}
	collectInfo = append(collectInfo, og.NewCollectIoTGrouped(noGroupedCollectInfo, f.deviceIdInBody, f.pathInBody, false)...)
	var dsNotMatched []odm_model.CollectDatastream
	for dsNameNotMatched, dpNotMatched := range rawFieldsDs {
		ds := odm_model.CollectDatastream{
			Id:         dsNameNotMatched,
			Datapoints: dpNotMatched,
		}
		dsNotMatched = append(dsNotMatched, ds)
	}
	if len(dsNotMatched) != 0 {
		if len(collectInfo) != 0 {
			collectInfo[0].Datastreams = append(collectInfo[0].Datastreams, dsNotMatched...)
		} else {
			collectInfo = []odm_model.CollectIot{{
				Version:     odm_model.Version,
				Device:      f.deviceIdInBody,
				Path:        f.pathInBody,
				Datastreams: dsNotMatched,
			}}
		}
	}
	return
}

// comprueba si existe ese filler previamente. Si no existe lo añade y si existe le agrega las metricas
func checkFiller(fillers []*Filler, newFiller Filler) []*Filler {
	for _, f := range fillers {
		if f.deviceIdInUri == newFiller.deviceIdInUri && strings.Join(f.pathInBody, "-") == strings.Join(newFiller.pathInBody, "-") && f.deviceIdInBody == newFiller.deviceIdInBody {
			f.metrics = append(f.metrics, newFiller.metrics...)
			return fillers
		}
	}
	return append(fillers, &newFiller)
}

//go:embed sample.conf
var sampleConfig string

// Define el nombre del plugin
func (o *OGReport) SampleConfig() string {
	return sampleConfig
}
func (o *OGReport) Connect() error {
	urlO, err := url.Parse(o.CollectUrl)
	if err != nil {
		return err
	}
	logger.NewConf(o.Log)
	toml_model := o.toModelOGList()
	if toml_model.ModelName != "" && len(toml_model.Relations) != 0 {
		og.AppendModel(toml_model)
	}
	models := og.GetCrudMatcher().GetAllModels()
	for _, model := range models {
		if model.ModelName == toml_model.ModelName {
			o.Log.Info("model " + model.ModelName + " loaded")
			for _, re := range model.Relations {
				var relationBuilder strings.Builder
				relationBuilder.WriteString(fmt.Sprintf("protocolName: %v | ds: %v", re.Field, re.OgDataStream))
				if re.Alias != "" {
					relationBuilder.WriteString(fmt.Sprintf(" | alias: %v", re.Alias))
				}
				if re.DataType != "" {
					relationBuilder.WriteString(fmt.Sprintf(" | dataType: %v", re.DataType))
				}
				if re.Factor != "" {
					relationBuilder.WriteString(fmt.Sprintf(" | factor: %v", re.Factor))
				}
				o.Log.Info(relationBuilder.String())
				for _, sub := range re.SubRelations {
					var subRelationBuilder strings.Builder
					subRelationBuilder.WriteString(fmt.Sprintf("--SubProtName: %v | ds: %v ", sub.Field, sub.OgDataStream))
					if sub.DataType != "" {
						subRelationBuilder.WriteString(fmt.Sprintf(" | dataType: %v", sub.DataType))
					}
					if sub.Factor != "" {
						subRelationBuilder.WriteString(fmt.Sprintf(" | factor: %v", sub.Factor))
					}
					o.Log.Info(subRelationBuilder.String())
				}
				for _, enum := range re.Enums {
					o.Log.Infof("--EnumProtValue: %v | EnumOGValue: %v \n", enum.CollectValue, enum.OGValue)
				}
			}
		}
	}
	path := urlO.Path
	prefix := strings.HasPrefix(path, "/north") || strings.HasPrefix(path, "/south")
	o.Log.Infof("Connected: Apikey: %v Username: %v FrontendUrl: %v\n", o.ApiKey, o.Username, o.CollectUrl)
	o.Log.Infof("host: %v protocolo: %v puerto: %v prefix: %v\n", urlO.Hostname(), urlO.Scheme, urlO.Port(), prefix)
	o.ClientRestOption = og_http.ClientOptions{
		OGRestOptions: og_http.OGRestOptions{
			Protocol:               urlO.Scheme,
			Host:                   urlO.Hostname(),
			Port:                   urlO.Port(),
			ClientTimes:            og_http.ClientTimes{TimeOutInCalls: time.Duration(o.Timeout), MaxRetries: 0},
			RemovePrefixNorthSouth: !prefix,
			ApiKey:                 o.ApiKey,
			TransPort:              &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		},
	}
	return nil
}
func (o *OGReport) Close() error {
	return nil
}

// Write should write immediately to the output, and not buffer writes
// (Telegraf manages the buffer for you). Returning an error will fail this
// batch of writes and the entire batch will be retried automatically.
func (o *OGReport) Write(metrics []telegraf.Metric) error {
	fillers := []*Filler{}
	for _, metric := range metrics {
		var deviceIdUri string = metric.Name()
		var deviceIdPath, deviceIdInBody string
		var pathInBody []string
		if o.DeviceInUriTagName != "" {
			if tagValue, found := metric.Tags()[o.DeviceInUriTagName]; found {
				deviceIdUri = tagValue
			}
		}
		if o.DeviceIdBodyTagName != "" {
			if tagValue, found := metric.Tags()[o.DeviceIdBodyTagName]; found {
				deviceIdInBody = tagValue
				deviceIdPath = deviceIdUri
			}
		}
		if o.DeviceInPathTagName != "" {
			if tagValue, found := metric.Tags()[o.DeviceInPathTagName]; found {
				deviceIdPath = tagValue
			}
		}
		o.Log.Debugf("DeviceInUri: %v | deviceInBody: %v | deviceInPath: %v", deviceIdUri, deviceIdInBody, deviceIdPath)
		if deviceIdPath != "" {
			pathInBody = []string{deviceIdPath}
		}
		newFiller := Filler{
			metrics:                []telegraf.Metric{metric},
			deviceIdInBody:         deviceIdInBody,
			pathInBody:             pathInBody,
			deviceIdInUri:          deviceIdUri,
			modelName:              o.Model.ModelName, //TODO ver como gestionar que haya mas de un modelo en el plugin. Aunque una opcion sea crearse otra entrada en el TOML del plugin con otra config nueva
			includeFieldNotMatched: o.IncludeFieldNotMatched,
		}
		fillers = checkFiller(fillers, newFiller)
	}
	for _, filler := range fillers {
		var manageError bool = false
		clientOptions := o.ClientRestOption
		clientOptions.DeviceId = filler.deviceIdInUri
		clientOptions.RequestId = fmt.Sprintf("%v", filler.metrics[0].HashID())
		normalizer := og.Normalizer{CollectionGenerator: filler, ClientOptions: clientOptions, ManageError: &manageError}
		//TODO ver como hacer en el caso de que se envien las X primeras recolecciones y que falle en una intermedia o la ultima
		if err := normalizer.SendCollectIoT(); err != nil {
			o.Log.Errorf("[error collecting]: %v", err)
			return err
		}
	}

	return nil
}

func (r *OGReport) toModelOGList() og.ModelOG {
	model := og.ModelOG{
		ModelName: r.Model.ModelName,
		Relations: make([]*og.Relation, 0, len(r.Model.Relations)),
	}
	for _, rc := range r.Model.Relations {
		var subRels []*og.SubRelation = nil
		var enums []*og.Enums = nil
		if len(rc.SubRelations) != 0 {
			subRels = make([]*og.SubRelation, 0, len(rc.SubRelations))
			for _, sr := range rc.SubRelations {
				subRels = append(subRels, &og.SubRelation{
					Field:        sr.Field,
					OgDataStream: sr.OgDataStream,
					DataType:     sr.DataType,
					Factor:       sr.Factor,
				})
			}
		}
		if len(rc.Enums) != 0 {
			enums = make([]*og.Enums, 0, len(rc.Enums))
			for _, e := range rc.Enums {
				enums = append(enums, &og.Enums{
					CollectValue: e.CollectValue,
					OGValue:      e.OGValue,
				})
			}
		}
		rel := &og.Relation{
			Field:        rc.Field,
			OgDataStream: rc.OgDataStream,
			Alias:        rc.Alias,
			DataType:     rc.DataType,
			Factor:       rc.Factor,
			SubRelations: subRels,
			Enums:        enums,
		}
		model.Relations = append(model.Relations, rel)
	}

	return model
}

func init() {
	outputs.Add("og_report", func() telegraf.Output { return &OGReport{} })
}
