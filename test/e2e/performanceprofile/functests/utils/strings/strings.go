package strings

import "strings"

func CleanDirName(dirName string) string {
	res := strings.ReplaceAll(dirName, "/", "|")
	res = strings.ReplaceAll(res, " ", "-")
	return res
}
