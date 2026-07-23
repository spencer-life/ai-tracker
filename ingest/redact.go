package ingest

import "regexp"

var secretRegexes = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-api[a-zA-Z0-9\-_]+`),
	regexp.MustCompile(`eyJ[a-zA-Z0-9\-_]+\.[a-zA-Z0-9\-_]+\.[a-zA-Z0-9\-_]+`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`dp\.pt\.[a-zA-Z0-9]+`),
}

func RedactSecrets(data []byte) []byte {
	for _, re := range secretRegexes {
		data = re.ReplaceAll(data, []byte("[REDACTED]"))
	}
	return data
}
