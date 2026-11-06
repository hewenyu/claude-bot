package lark

// AppKey names the app c connects to, by its app id.
func (c Config) AppKey() string {
	return Name + ":" + c.AppID
}
