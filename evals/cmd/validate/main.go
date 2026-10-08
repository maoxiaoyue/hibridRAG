// evalvalidate — lint eval dataset（*.yaml）：格式錯誤在這裡擋下，
// 不要讓 evalrun 跑到一半才因為缺欄位而困惑地失敗。
//
//	go run ./cmd/validate -cases cases
package main

import (
	"flag"
	"fmt"
	"os"

	"evals/pkg/dataset"
)

func main() {
	casesDir := flag.String("cases", "cases", "eval 案例目錄（*.yaml）")
	flag.Parse()

	cases, err := dataset.LoadDir(*casesDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load dataset: %v\n", err)
		os.Exit(1)
	}

	errs := dataset.Validate(cases)
	if len(errs) == 0 {
		fmt.Printf("OK: %d case(s) in %s, no errors\n", len(cases), *casesDir)
		return
	}

	for _, e := range errs {
		fmt.Fprintf(os.Stderr, "%v\n", e)
	}
	fmt.Fprintf(os.Stderr, "\n%d case(s) loaded, %d error(s)\n", len(cases), len(errs))
	os.Exit(1)
}
