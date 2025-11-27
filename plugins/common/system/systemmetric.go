package system

import "time"

type SystemMetric interface {
	TelegrafNormalize() TelegrafEvent
}

// TODO sujero a cambios: Esta es la estructura que representara el envio de datos al telegraf
type TelegrafEvent struct {
	Fields   map[string]interface{}
	Tags     map[string]string
	DeviceID string
	Time     time.Time
}

func (t *TelegrafEvent) GetDeviceID() string {
	return t.DeviceID
}
func (t *TelegrafEvent) GetFields() map[string]interface{} {
	return t.Fields
}
func (t *TelegrafEvent) GetTags() map[string]string {
	return t.Tags
}
func (t *TelegrafEvent) GetTime() time.Time {
	return t.Time
}
