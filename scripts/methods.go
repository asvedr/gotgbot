package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/template"
)

func generateMethods(d APIDescription) error {
	file := strings.Builder{}
	file.WriteString(`
// THIS FILE IS AUTOGENERATED. DO NOT EDIT.
// Regen by running 'go generate' in the repo root.

package gen
import (
	urlLib "net/url" // renamed to avoid clashes with url vars
	"encoding/json"
	"strconv"
	"fmt"
	"io"
)
`)

	// TODO: Obtain ordered map to retain tg ordering
	var methods []string
	for k := range d.Methods {
		methods = append(methods, k)
	}
	sort.Strings(methods)

	for _, tgMethodName := range methods {
		tgMethod := d.Methods[tgMethodName]
		method, err := generateMethodDef(d, tgMethod, tgMethodName)
		if err != nil {
			return fmt.Errorf("failed to generate method definition of %s: %w", tgMethodName, err)
		}
		file.WriteString(method)
	}

	return writeGenToFile(file, "gen/gen_methods.go")
}

func generateMethodDef(d APIDescription, tgMethod MethodDescription, tgMethodName string) (string, error) {
	method := strings.Builder{}

	// defaulting to [0] is ok because its either message or bool
	retType := toGoType(tgMethod.Returns[0])
	if isTgType(d.Types, retType) {
		retType = "*" + retType
	}
	defaultRetVal := getDefaultReturnVal(retType)

	args, optionalsStruct, err := getArgs(tgMethodName, tgMethod)
	if err != nil {
		return "", fmt.Errorf("failed to get args for method %s: %w", tgMethodName, err)
	}

	if optionalsStruct != "" {
		method.WriteString("\n" + optionalsStruct)
	}

	for _, d := range tgMethod.Description {
		method.WriteString("\n// " + d)
	}
	method.WriteString("\n// " + tgMethod.Href)

	method.WriteString("\nfunc (bot Bot) " + strings.Title(tgMethodName) + "(" + args + ") (" + retType + ", error) {")

	valueGen, hasData, err := methodArgsToValues(tgMethod, defaultRetVal)
	if err != nil {
		return "", fmt.Errorf("failed to generate url values for method %s: %w", tgMethodName, err)
	}

	method.WriteString("\n	v := urlLib.Values{}")
	if hasData {
		method.WriteString("\n	data := map[string]NamedReader{}")
	}

	method.WriteString(valueGen)
	method.WriteString("\n")

	if hasData {
		method.WriteString("\nr, err := bot.Post(\"" + tgMethodName + "\", v, data)")
	} else {
		method.WriteString("\nr, err := bot.Get(\"" + tgMethodName + "\", v)")
	}
	method.WriteString("\n	if err != nil {")
	method.WriteString("\n		return " + defaultRetVal + ", err")
	method.WriteString("\n	}")
	method.WriteString("\n")

	retVarType := retType
	retVarName := getRetVarName(retVarType)
	isPointer := strings.HasPrefix(retVarType, "*")
	addr := ""
	if isPointer {
		retVarType = strings.TrimLeft(retVarType, "*")
		addr = "&"
	}
	method.WriteString("\nvar " + retVarName + " " + retVarType)
	method.WriteString("\nreturn " + addr + retVarName + ", json.Unmarshal(r, &" + retVarName + ")")
	method.WriteString("\n}")

	return method.String(), nil
}

func methodArgsToValues(method MethodDescription, defaultRetVal string) (string, bool, error) {
	hasData := false
	bd := strings.Builder{}
	for _, f := range method.Fields {
		goParam := snakeToCamel(f.Name)
		if !f.Required {
			goParam = "opts." + snakeToTitle(f.Name)
		}

		fieldType, err := getPreferredType(f)
		if err != nil {
			return "", false, fmt.Errorf("failed to get preferred type: %w", err)
		}
		stringer := goTypeStringer(toGoType(fieldType))
		if stringer != "" {
			bd.WriteString("\nv.Add(\"" + f.Name + "\", " + fmt.Sprintf(stringer, goParam) + ")")
			continue
		}

		switch fieldType {
		case "InputFile":
			hasData = true

			tmplString := stringOrReaderBranch
			if len(f.Types) == 1 {
				// This is actually just an inputfile, not "InputFile or String", so don't support string
				tmplString = readerBranch
			}

			t, err := template.New("readers").Parse(tmplString)
			if err != nil {
				return "", false, fmt.Errorf("failed to parse template: %w", err)
			}

			err = t.Execute(&bd, readerBranchesData{
				GoParam:       goParam,
				DefaultReturn: defaultRetVal,
				Parameter:     f.Name,
			})
			if err != nil {
				return "", false, fmt.Errorf("failed to execute template: %w", err)
			}

		case "InputMedia":
			hasData = true

			bd.WriteString("\ninputMediaBs, err := " + goParam + ".InputMediaParams(\"" + f.Name + "\" , data)")
			bd.WriteString("\nif err != nil {")
			bd.WriteString("\n	return " + defaultRetVal + ", fmt.Errorf(\"failed to marshal field " + f.Name + ": %w\", err)")
			bd.WriteString("\n}")
			bd.WriteString("\nv.Add(\"" + f.Name + "\", string(inputMediaBs))")

		case "Array of InputMedia":
			hasData = true

			var v []json.RawMessage
			v = append(v, []byte{'a'})
			bd.WriteString("\nif " + goParam + " != nil {")
			bd.WriteString("\n	var rawList []json.RawMessage")
			bd.WriteString("\n	for idx, im := range " + goParam + " {")
			bd.WriteString("\n		inputMediaBs, err := im.InputMediaParams(\"" + f.Name + "\" + strconv.Itoa(idx), data)")
			bd.WriteString("\n		if err != nil {")
			bd.WriteString("\n			return " + defaultRetVal + ", fmt.Errorf(\"failed to marshal InputMedia list item %d for field " + f.Name + ": %w\", idx, err)")
			bd.WriteString("\n		}")
			bd.WriteString("\n		rawList = append(rawList, inputMediaBs)")
			bd.WriteString("\n	}")
			bd.WriteString("\n	bytes, err := json.Marshal(rawList)")
			bd.WriteString("\n	if err != nil {")
			bd.WriteString("\n		return " + defaultRetVal + ", fmt.Errorf(\"failed to marshal raw json list of InputMedia for field: " + f.Name + " %w\", err)")
			bd.WriteString("\n	}")
			bd.WriteString("\n	v.Add(\"" + f.Name + "\", string(bytes))")
			bd.WriteString("\n}")

		case "ReplyMarkup":
			bd.WriteString("\n	bytes, err := " + goParam + ".ReplyMarkup()")
			bd.WriteString("\n	if err != nil {")
			bd.WriteString("\n		return " + defaultRetVal + ", fmt.Errorf(\"failed to marshal field " + f.Name + ": %w\", err)")
			bd.WriteString("\n	}")
			bd.WriteString("\n	v.Add(\"" + f.Name + "\", string(bytes))")

		default:
			if isTgArray(fieldType) {
				bd.WriteString("\nif " + goParam + " != nil {")
			}

			bd.WriteString("\n	bytes, err := json.Marshal(" + goParam + ")")
			bd.WriteString("\n	if err != nil {")
			bd.WriteString("\n		return " + defaultRetVal + ", fmt.Errorf(\"failed to marshal field " + f.Name + ": %w\", err)")
			bd.WriteString("\n	}")
			bd.WriteString("\n	v.Add(\"" + f.Name + "\", string(bytes))")

			if isTgArray(fieldType) {
				bd.WriteString("\n}")
			}
		}
	}

	return bd.String(), hasData, nil
}

func getRetVarName(retType string) string {
	for strings.HasPrefix(retType, "*") {
		retType = strings.TrimPrefix(retType, "*")
	}
	for strings.HasPrefix(retType, "[]") {
		retType = strings.TrimPrefix(retType, "[]")
	}
	return strings.ToLower(retType[:1])
}

func getArgs(name string, method MethodDescription) (string, string, error) {
	var requiredArgs []string
	optionals := strings.Builder{}
	for _, f := range method.Fields {
		fieldType, err := getPreferredType(f)
		if err != nil {
			return "", "", fmt.Errorf("failed to get preferred type: %w", err)
		}
		goType := toGoType(fieldType)
		if f.Required {
			requiredArgs = append(requiredArgs, fmt.Sprintf("%s %s", snakeToCamel(f.Name), goType))
			continue
		}

		optionals.WriteString("\n// " + f.Description)
		optionals.WriteString("\n" + fmt.Sprintf("%s %s", snakeToTitle(f.Name), goType))

	}
	optionalsStruct := ""

	if optionals.Len() > 0 {
		optionalsName := snakeToTitle(name) + "Opts"
		bd := strings.Builder{}
		bd.WriteString("\ntype " + optionalsName + " struct {")
		bd.WriteString(optionals.String())
		bd.WriteString("\n}")
		optionalsStruct = bd.String()

		requiredArgs = append(requiredArgs, fmt.Sprintf("opts %s", optionalsName))
	}

	return strings.Join(requiredArgs, ", "), optionalsStruct, nil
}

type readerBranchesData struct {
	GoParam       string
	DefaultReturn string
	Parameter     string
}

const readerBranch = `
if {{.GoParam}} != nil {
	if r, ok := {{.GoParam}}.(io.Reader); ok {
		v.Add("{{.Parameter}}", "attach://{{.Parameter}}")
		data["{{.Parameter}}"] = NamedReader{File: r}
	} else if nf, ok := {{.GoParam}}.(NamedReader); ok {
		v.Add("{{.Parameter}}", "attach://{{.Parameter}}")
		data["{{.Parameter}}"] = nf
	} else {
		return {{.DefaultReturn}}, fmt.Errorf("unknown type for InputFile: %T",{{.GoParam}})
	}
}
`

const stringOrReaderBranch = `
if {{.GoParam}} != nil {
	if s, ok := {{.GoParam}}.(string); ok {
		v.Add("{{.Parameter}}", s)
	} else if r, ok := {{.GoParam}}.(io.Reader); ok {
		v.Add("{{.Parameter}}", "attach://{{.Parameter}}")
		data["{{.Parameter}}"] = NamedReader{File: r}
	} else if nf, ok := {{.GoParam}}.(NamedReader); ok {
		v.Add("{{.Parameter}}", "attach://{{.Parameter}}")
		data["{{.Parameter}}"] = nf
	} else {
		return {{.DefaultReturn}}, fmt.Errorf("unknown type for InputFile: %T",{{.GoParam}})
	}
}
`
