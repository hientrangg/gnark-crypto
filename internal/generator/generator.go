package generator

import (
	"fmt"
	"math/big"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/consensys/bavard"
	goff "github.com/consensys/goff/cmd"
	"github.com/consensys/gurvy/internal/templates/fq12over6over2"
	"github.com/consensys/gurvy/internal/templates/pairing"
	"github.com/consensys/gurvy/internal/templates/point"
)

// CurveConfig describes parameters of the curve useful for the templates
type CurveConfig struct {
	CurveName string
	RTorsion  string
	RBitLen   int
	FpModulus string
	OutputDir string
	GLV       bool  // scalar mulitplication using GLV
	CRange    []int // multiexp bucket method: generate inner methods (with const arrays) for each c
	pMod4     int   // 3 or 1
}

type PointConfig struct {
	CurveConfig
	CoordType string
	PointName string
}

func NewCurveConfig(name, rTorsion, fpModulus string, glv bool) CurveConfig {
	name = strings.ToLower(name)
	conf := CurveConfig{
		CurveName: name,
		RTorsion:  rTorsion,
		FpModulus: fpModulus,
		GLV:       glv,
	}

	conf.OutputDir = fmt.Sprintf("../%s/", name)

	// bit len of R
	r, ok := new(big.Int).SetString(rTorsion, 10)
	if !ok {
		panic("can't set r from RTorsion")
	}
	conf.RBitLen = r.BitLen()
	for conf.RBitLen%64 != 0 {
		conf.RBitLen++
	}

	// sets the residue of p mod 4
	r, ok = new(big.Int).SetString(fpModulus, 10)
	if !ok {
		panic("can't parse fpModulus")
	}
	b := r.Bytes()
	conf.pMod4 = int(b[len(b)-1] & 3)

	// default range for C values in the multiExp
	conf.CRange = []int{4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 20, 21, 22}
	return conf
}

// GenerateBaseFields generates the base field fr and fp
func GenerateBaseFields(conf CurveConfig) error {
	if err := goff.GenerateFF("fr", "Element", conf.RTorsion, filepath.Join(conf.OutputDir, "fr"), false); err != nil {
		return err
	}
	if err := goff.GenerateFF("fp", "Element", conf.FpModulus, filepath.Join(conf.OutputDir, "fp"), false); err != nil {
		return err
	}
	return nil
}

// GenerateFq12over6over2 generates a tower 2->6->12 over fp
func GenerateFq12over6over2(conf CurveConfig) error {

	// fq2 base
	src := []string{
		fq12over6over2.Fq2Common,
	}

	bavardOpts := []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
		bavard.GeneratedBy("gurvy"),
	}
	pathSrc := filepath.Join(conf.OutputDir, "e2.go")

	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	{
		// fq2 assembly, generated by goff
		if err := goff.GenerateFF2(conf.CurveName, "E2", conf.FpModulus, conf.OutputDir); err != nil {
			return err
		}
	}

	{
		// fq2 fallback
		src := []string{
			fq12over6over2.Fq2FallBack,
		}

		bavardOpts := []func(*bavard.Bavard) error{
			bavard.Apache2("ConsenSys AG", 2020),
			bavard.Package(conf.CurveName),
			bavard.GeneratedBy("gurvy"),
			bavard.BuildTag("!amd64"),
		}
		pathSrc := filepath.Join(conf.OutputDir, "e2_fallback.go")

		if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
			return err
		}

	}

	// fq2 tests
	src = []string{
		fq12over6over2.Fq2Tests,
	}
	pathSrc = filepath.Join(conf.OutputDir, "e2_test.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// fq6 base
	src = []string{
		fq12over6over2.Fq6,
	}

	bavardOpts = []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
	}

	pathSrc = filepath.Join(conf.OutputDir, "e6.go")

	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// fq6 tests
	src = []string{
		fq12over6over2.Fq6Tests,
	}
	pathSrc = filepath.Join(conf.OutputDir, "e6_test.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// fq12 base
	src = []string{
		fq12over6over2.Fq12,
	}

	bavardOpts = []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
	}

	pathSrc = filepath.Join(conf.OutputDir, "e12.go")

	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// fq12 tests
	src = []string{
		fq12over6over2.Fq12Tests,
	}
	pathSrc = filepath.Join(conf.OutputDir, "e12_test.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	return nil
}

// Template helpers (txt/template)
func helpers() template.FuncMap {
	// functions used in template
	return template.FuncMap{
		"divides": divides,
	}
}

// return true if c1 divides c2, that is, c2 % c1 == 0
func divides(c1, c2 interface{}) bool {
	switch cc1 := c1.(type) {
	case int:
		switch cc2 := c2.(type) {
		case int:
			return cc2%cc1 == 0
		case string:
			c2Int, err := strconv.Atoi(cc2)
			if err != nil {
				panic(err)
			}
			return c2Int%cc1 == 0
		}
	case string:
		c1Int, err := strconv.Atoi(cc1)
		if err != nil {
			panic(err)
		}
		switch cc2 := c2.(type) {
		case int:
			return cc2%c1Int == 0
		case string:
			c2Int, err := strconv.Atoi(cc2)
			if err != nil {
				panic(err)
			}
			return c2Int%c1Int == 0
		}
	}
	panic("unexpected type")
}

// GenerateMultiExpHelpers generates multi exp helpers functions
func GenerateMultiExpHelpers(conf CurveConfig) error {

	bavardOpts := []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
		bavard.GeneratedBy("gurvy"),
		bavard.Funcs(helpers()),
	}

	// point code
	src := []string{
		point.MultiExpHelpers,
	}

	pathSrc := filepath.Join(conf.OutputDir, "multiexp_helpers.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	return nil
}

// GeneratePoint generates elliptic curve arithmetic
func GeneratePoint(_conf CurveConfig, coordType, pointName string) error {

	conf := PointConfig{
		CurveConfig: _conf,
		CoordType:   coordType,
		PointName:   pointName,
	}

	bavardOpts := []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
		bavard.GeneratedBy("gurvy"),
		bavard.Funcs(helpers()),
	}

	// point code (without multi exp)
	src := []string{
		point.Point,
	}

	pathSrc := filepath.Join(conf.OutputDir, conf.PointName+".go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// multi exp core code
	src = []string{
		point.MultiExpCore,
	}
	pathSrc = filepath.Join(conf.OutputDir, conf.PointName+"_multiexp.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	// point test
	src = []string{
		point.PointTests,
	}

	pathSrc = filepath.Join(conf.OutputDir, conf.PointName+"_test.go")
	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	return nil
}

// GeneratePairingTests generates elliptic curve arithmetic
func GeneratePairingTests(conf CurveConfig) error {

	src := []string{
		pairing.PairingTests,
	}

	bavardOpts := []func(*bavard.Bavard) error{
		bavard.Apache2("ConsenSys AG", 2020),
		bavard.Package(conf.CurveName),
		bavard.GeneratedBy("gurvy"),
	}

	pathSrc := filepath.Join(conf.OutputDir, "pairing_test.go")

	if err := bavard.Generate(pathSrc, src, conf, bavardOpts...); err != nil {
		return err
	}

	return nil
}
