package agentcfg

// ProvisionPassword reads a staged mailbox credential for the provisioning
// service only. Never expose this method through a transport or general file API.
func (d *Dir) ProvisionPassword(envFile, key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	lines, e := d.envValues(envFile)
	if e != nil {
		return "", e
	}
	for _, l := range lines {
		if l.key == key {
			return l.value, nil
		}
	}
	return "", nil
}
