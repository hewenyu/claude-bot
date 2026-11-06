package dingtalk

// AppKey names the app c connects to, by its client id.
func (c Config) AppKey() string {
	return Name + ":" + c.ClientID
}
