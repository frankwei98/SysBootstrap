package modules

func boolWord(ok bool, yes, no string) string {
	if ok {
		return yes
	}
	return no
}
