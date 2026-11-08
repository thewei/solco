package helperipc

import "os"

func osStat(path string) (uint32, error) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	return uint32(st.Mode().Perm()), nil
}
