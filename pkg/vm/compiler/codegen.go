package compiler

import (
	"bytes"
	"encoding/binary"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"log"

	"github.com/CityOfZion/neo-go/pkg/vm"
)

const mainIdent = "Main"

type codegen struct {
	// Information about the program with all its dependencies.
	buildInfo *buildInfo

	// prog holds the output buffer
	prog *bytes.Buffer

	// Type information
	typeInfo *types.Info

	// A mapping of func identifiers with their scope.
	funcs map[string]*funcScope

	// Current funcScope being converted.
	scope *funcScope

	// Label table for recording jump destinations.
	l []int
}

// newLabel creates a new label to jump to
func (c *codegen) newLabel() (l int) {
	l = len(c.l)
	c.l = append(c.l, -1)
	return
}

func (c *codegen) setLabel(l int) {
	c.l[l] = c.pc() + 1
}

// pc return the program offset off the last instruction.
func (c *codegen) pc() int {
	return c.prog.Len() - 1
}

func (c *codegen) emitLoadConst(t types.TypeAndValue) {
	switch typ := t.Type.Underlying().(type) {
	case *types.Basic:
		switch typ.Kind() {
		case types.Int, types.UntypedInt:
			val, _ := constant.Int64Val(t.Value)
			emitInt(c.prog, val)
		case types.String, types.UntypedString:
			val := constant.StringVal(t.Value)
			emitString(c.prog, val)
		case types.Bool, types.UntypedBool:
			val := constant.BoolVal(t.Value)
			emitBool(c.prog, val)
		case types.Byte:
			val, _ := constant.Int64Val(t.Value)
			b := byte(val)
			emitBytes(c.prog, []byte{b})
		default:
			log.Fatalf("compiler don't know how to convert this basic type: %v", t)
		}
	default:
		log.Fatalf("compiler don't know how to convert this constant: %v", t)
	}
}

func (c *codegen) emitLoadLocal(name string) {
	pos := c.scope.loadLocal(name)
	if pos < 0 {
		log.Fatalf("cannot load local variable with position: %d", pos)
	}

	emitOpcode(c.prog, vm.Ofromaltstack)
	emitOpcode(c.prog, vm.Odup)
	emitOpcode(c.prog, vm.Otoaltstack)

	emitInt(c.prog, int64(pos))
	emitOpcode(c.prog, vm.Opickitem)
}

func (c *codegen) emitStoreLocal(pos int) {
	emitOpcode(c.prog, vm.Ofromaltstack)
	emitOpcode(c.prog, vm.Odup)
	emitOpcode(c.prog, vm.Otoaltstack)

	if pos < 0 {
		log.Fatalf("invalid position to store local: %d", pos)
	}

	emitInt(c.prog, int64(pos))
	emitInt(c.prog, 2)
	emitOpcode(c.prog, vm.Oroll)
	emitOpcode(c.prog, vm.Osetitem)
}

func (c *codegen) emitLoadStructField(i int) {
	emitInt(c.prog, int64(i))
	emitOpcode(c.prog, vm.Opickitem)
}

func (c *codegen) emitStoreStructField(i int) {
	emitInt(c.prog, int64(i))
	emitOpcode(c.prog, vm.Orot)
	emitOpcode(c.prog, vm.Osetitem)
}

func (c *codegen) emitSyscallReturn() {
	emitOpcode(c.prog, vm.Ojmp)
	emitOpcode(c.prog, vm.Opcode(0x03))
	emitOpcode(c.prog, vm.Opush0)

	emitInt(c.prog, int64(0))

	emitOpcode(c.prog, vm.Onop)
	emitOpcode(c.prog, vm.Ofromaltstack)
	emitOpcode(c.prog, vm.Odrop)
	emitOpcode(c.prog, vm.Oret)
}

// convertGlobals will traverse the AST and only convert global declarations.
// If we call this in convertFuncDecl then it will load all global variables
// into the scope of the function.
func (c *codegen) convertGlobals(f *ast.File) {
	ast.Inspect(f, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.FuncDecl:
			return false
		case *ast.GenDecl:
			ast.Walk(c, n)
		}
		return true
	})
}

func (c *codegen) convertFuncDecl(file *ast.File, decl *ast.FuncDecl) {
	var (
		f  *funcScope
		ok bool
	)

	f, ok = c.funcs[decl.Name.Name]
	if ok {
		c.setLabel(f.label)
	} else {
		f = c.newFunc(decl)
	}

	c.scope = f
	ast.Inspect(decl, c.scope.analyzeVoidCalls)

	// All globals copied into the scope of the function need to be added
	// to the stack size of the function.
	emitInt(c.prog, f.stackSize()+countGlobals(file))
	emitOpcode(c.prog, vm.Onewarray)
	emitOpcode(c.prog, vm.Otoaltstack)

	// We need to handle methods, which in Go, is just syntactic sugar.
	// The method receiver will be passed in as first argument.
	// We check if this declaration has a receiver and load it into scope.
	//
	// FIXME: For now we will hard cast this to a struct. We can later finetune this
	// to support other types.
	if decl.Recv != nil {
		for _, arg := range decl.Recv.List {
			ident := arg.Names[0]
			// Currently only method receives for struct types is supported.
			_, ok := c.typeInfo.Defs[ident].Type().Underlying().(*types.Struct)
			if !ok {
				log.Fatal("method receives for non-struct types is not yet supported")
			}
			l := c.scope.newLocal(ident.Name)
			c.emitStoreLocal(l)
		}
	}

	// Load the arguments in scope.
	for _, arg := range decl.Type.Params.List {
		name := arg.Names[0].Name // for now.
		l := c.scope.newLocal(name)
		c.emitStoreLocal(l)
	}

	// If this function is a syscall we will manipulate the return value to 0.
	// All the syscalls are just signatures functions and bring no real return value.
	// The return values you will find in the smartcontract package is just for
	// satisfying the typechecker and the user experience.
	if isSyscall(f.name) {
		c.emitSyscallReturn()
	} else {
		// After loading the arguments we can convert the globals into the scope of the function.
		c.convertGlobals(file)
		ast.Walk(c, decl.Body)
	}
}

func (c *codegen) Visit(node ast.Node) ast.Visitor {
	switch n := node.(type) {

	// General declarations.
	// var (
	//     x = 2
	// )
	case *ast.GenDecl:
		for _, spec := range n.Specs {
			switch t := spec.(type) {
			case *ast.ValueSpec:
				for i, val := range t.Values {
					ast.Walk(c, val)
					l := c.scope.newLocal(t.Names[i].Name)
					c.emitStoreLocal(l)
				}
			}
		}
		return nil

	case *ast.AssignStmt:
		for i := 0; i < len(n.Lhs); i++ {
			switch t := n.Lhs[i].(type) {
			case *ast.Ident:
				switch n.Tok {
				case token.ADD_ASSIGN, token.SUB_ASSIGN, token.MUL_ASSIGN, token.QUO_ASSIGN:
					c.emitLoadLocal(t.Name)
					ast.Walk(c, n.Rhs[0]) // can only add assign to 1 expr on the RHS
					c.convertToken(n.Tok)
					l := c.scope.loadLocal(t.Name)
					c.emitStoreLocal(l)
				default:
					ast.Walk(c, n.Rhs[i])
					l := c.scope.loadLocal(t.Name)
					c.emitStoreLocal(l)
				}

			case *ast.SelectorExpr:
				switch expr := t.X.(type) {
				case *ast.Ident:
					ast.Walk(c, n.Rhs[i])
					typ := c.typeInfo.ObjectOf(expr).Type().Underlying()
					if strct, ok := typ.(*types.Struct); ok {
						c.emitLoadLocal(expr.Name)            // load the struct
						i := indexOfStruct(strct, t.Sel.Name) // get the index of the field
						c.emitStoreStructField(i)             // store the field
					}
				default:
					log.Fatal("nested selector assigns not supported yet")
				}
			}
		}
		return nil

	case *ast.ReturnStmt:
		if len(n.Results) > 1 {
			log.Fatal("multiple returns not supported.")
		}

		emitOpcode(c.prog, vm.Ojmp)
		emitOpcode(c.prog, vm.Opcode(0x03))
		emitOpcode(c.prog, vm.Opush0)

		if len(n.Results) > 0 {
			ast.Walk(c, n.Results[0])
		}

		emitOpcode(c.prog, vm.Onop)
		emitOpcode(c.prog, vm.Ofromaltstack)
		emitOpcode(c.prog, vm.Odrop)
		emitOpcode(c.prog, vm.Oret)
		return nil

	case *ast.IfStmt:
		lIf := c.newLabel()
		lElse := c.newLabel()
		if n.Cond != nil {
			ast.Walk(c, n.Cond)
			emitJmp(c.prog, vm.Ojmpifnot, int16(lElse))
		}

		c.setLabel(lIf)
		ast.Walk(c, n.Body)

		if n.Else != nil {
			// TODO: handle else statements.
			// emitJmp(c.prog, vm.Ojmp, int16(lEnd))
		}
		c.setLabel(lElse)
		if n.Else != nil {
			ast.Walk(c, n.Else)
		}
		return nil

	case *ast.BasicLit:
		c.emitLoadConst(c.typeInfo.Types[n])
		return nil

	case *ast.Ident:
		if isIdentBool(n) {
			c.emitLoadConst(makeBoolFromIdent(n, c.typeInfo))
		} else {
			c.emitLoadLocal(n.Name)
		}
		return nil

	case *ast.CompositeLit:
		var typ types.Type

		switch t := n.Type.(type) {
		case *ast.Ident:
			typ = c.typeInfo.ObjectOf(t).Type().Underlying()
		case *ast.SelectorExpr:
			typ = c.typeInfo.ObjectOf(t.Sel).Type().Underlying()
		default:
			ln := len(n.Elts)
			// ByteArrays need a different approach then normal arrays.
			if isByteArray(n, c.typeInfo) {
				c.convertByteArray(n)
				return nil
			}
			for i := ln - 1; i >= 0; i-- {
				c.emitLoadConst(c.typeInfo.Types[n.Elts[i]])
			}
			emitInt(c.prog, int64(ln))
			emitOpcode(c.prog, vm.Opack)
			return nil
		}

		switch typ.(type) {
		case *types.Struct:
			c.convertStruct(n)
		}

		return nil

	case *ast.BinaryExpr:
		switch n.Op {
		case token.LAND:
			ast.Walk(c, n.X)
			emitJmp(c.prog, vm.Ojmpifnot, int16(len(c.l)-1))
			ast.Walk(c, n.Y)
			return nil

		case token.LOR:
			ast.Walk(c, n.X)
			emitJmp(c.prog, vm.Ojmpif, int16(len(c.l)-2))
			ast.Walk(c, n.Y)
			return nil

		default:
			// The AST package will try to resolve all basic literals for us.
			// If the typeinfo.Value is not nil we know that the expr is resolved
			// and needs no further action. e.g. x := 2 + 2 + 2 will be resolved to 6.
			// NOTE: Constants will also be automagically resolved be the AST parser.
			// example:
			// const x = 10
			// x + 2 will results into 12
			if tinfo := c.typeInfo.Types[n]; tinfo.Value != nil {
				c.emitLoadConst(tinfo)
				return nil
			}

			ast.Walk(c, n.X)
			ast.Walk(c, n.Y)
			c.convertToken(n.Op)
			return nil
		}

	case *ast.CallExpr:
		var (
			f       *funcScope
			ok      bool
			numArgs = len(n.Args)
		)

		switch fun := n.Fun.(type) {
		case *ast.Ident:
			f, ok = c.funcs[fun.Name]
			if !ok {
				log.Fatalf("could not resolve function %s", fun.Name)
			}
		case *ast.SelectorExpr:
			// If this is a method call we need to walk the AST to load the struct locally.
			// Otherwise this is a function call from a imported package and we can call it
			// directly.
			if c.typeInfo.Selections[fun] != nil {
				ast.Walk(c, fun.X)
				// Dont forget to add 1 extra argument when its a method.
				numArgs++
			}
			f, ok = c.funcs[fun.Sel.Name]
			if !ok {
				log.Fatalf("could not resolve function %s", fun.Sel.Name)
			}
		}

		// Handle the arguments
		for _, arg := range n.Args {
			ast.Walk(c, arg)
		}
		if numArgs == 2 {
			emitOpcode(c.prog, vm.Oswap)
		}
		if numArgs == 3 {
			emitInt(c.prog, 2)
			emitOpcode(c.prog, vm.Oxswap)
		}

		// c# compiler adds a NOP (0x61) before every function call. Dont think its relevant
		// and we could easily removed it, but to be consistent with the original compiler I
		// will put them in. ^^
		emitOpcode(c.prog, vm.Onop)

		if isSyscall(f.name) {
			c.convertSyscall(f.name)
		} else {
			emitCall(c.prog, vm.Ocall, int16(f.label))
		}

		// If we are not assigning this function to a variable we need to drop
		// the top stack item. It's not a void but you get the point \o/.
		if _, ok := c.scope.voidCalls[n]; ok && !isNoRetSyscall(f.name) {
			emitOpcode(c.prog, vm.Odrop)
		}
		return nil

	case *ast.SelectorExpr:
		switch t := n.X.(type) {
		case *ast.Ident:
			typ := c.typeInfo.ObjectOf(t).Type().Underlying()
			if strct, ok := typ.(*types.Struct); ok {
				c.emitLoadLocal(t.Name) // load the struct
				i := indexOfStruct(strct, n.Sel.Name)
				c.emitLoadStructField(i) // load the field
			}
		default:
			log.Fatal("nested selectors not supported yet")
		}
		return nil

	case *ast.UnaryExpr:
		// fmt.Println(n)
	}
	return c
}

func (c *codegen) convertSyscall(name string) {
	api, ok := vm.Syscalls[name]
	if !ok {
		log.Fatalf("unknown VM syscall api: %s", name)
	}
	emitSyscall(c.prog, api)
	emitOpcode(c.prog, vm.Onop)
}

func (c *codegen) convertByteArray(lit *ast.CompositeLit) {
	buf := make([]byte, len(lit.Elts))
	for i := 0; i < len(lit.Elts); i++ {
		t := c.typeInfo.Types[lit.Elts[i]]
		val, _ := constant.Int64Val(t.Value)
		buf[i] = byte(val)
	}
	emitBytes(c.prog, buf)
}

func (c *codegen) convertStruct(lit *ast.CompositeLit) {
	// Create a new structScope to initialize and store
	// the positions of its variables.
	strct, ok := c.typeInfo.TypeOf(lit).Underlying().(*types.Struct)
	if !ok {
		log.Fatalf("the given literal is not of type struct: %v", lit)
	}

	emitOpcode(c.prog, vm.Onop)
	emitInt(c.prog, int64(strct.NumFields()))
	emitOpcode(c.prog, vm.Onewstruct)
	emitOpcode(c.prog, vm.Otoaltstack)

	// We need to locally store all the fields, even if they are not initialized.
	// We will initialize all fields to their "zero" value.
	for i := 0; i < strct.NumFields(); i++ {
		sField := strct.Field(i)
		fieldAdded := false

		// Fields initialized by the program.
		for _, field := range lit.Elts {
			f := field.(*ast.KeyValueExpr)
			fieldName := f.Key.(*ast.Ident).Name

			if sField.Name() == fieldName {
				ast.Walk(c, f.Value)
				pos := indexOfStruct(strct, fieldName)
				c.emitStoreLocal(pos)
				fieldAdded = true
				break
			}
		}
		if fieldAdded {
			continue
		}

		typeAndVal := typeAndValueForField(sField)
		c.emitLoadConst(typeAndVal)
		c.emitStoreLocal(i)
	}
	emitOpcode(c.prog, vm.Ofromaltstack)
}

func (c *codegen) convertToken(tok token.Token) {
	switch tok {
	case token.ADD_ASSIGN:
		emitOpcode(c.prog, vm.Oadd)
	case token.SUB_ASSIGN:
		emitOpcode(c.prog, vm.Osub)
	case token.MUL_ASSIGN:
		emitOpcode(c.prog, vm.Omul)
	case token.QUO_ASSIGN:
		emitOpcode(c.prog, vm.Odiv)
	case token.ADD:
		emitOpcode(c.prog, vm.Oadd)
	case token.SUB:
		emitOpcode(c.prog, vm.Osub)
	case token.MUL:
		emitOpcode(c.prog, vm.Omul)
	case token.QUO:
		emitOpcode(c.prog, vm.Odiv)
	case token.LSS:
		emitOpcode(c.prog, vm.Olt)
	case token.LEQ:
		emitOpcode(c.prog, vm.Olte)
	case token.GTR:
		emitOpcode(c.prog, vm.Ogt)
	case token.GEQ:
		emitOpcode(c.prog, vm.Ogte)
	case token.EQL, token.NEQ:
		emitOpcode(c.prog, vm.Onumequal)
	default:
		log.Fatalf("compiler could not convert token: %s", tok)
	}
}

func (c *codegen) newFunc(decl *ast.FuncDecl) *funcScope {
	f := newFuncScope(decl, c.newLabel())
	c.funcs[f.name] = f
	return f
}

// CodeGen is the function that compiles the program to bytecode.
func CodeGen(info *buildInfo) (*bytes.Buffer, error) {
	pkg := info.program.Package(info.initialPackage)
	c := &codegen{
		buildInfo: info,
		prog:      new(bytes.Buffer),
		l:         []int{},
		funcs:     map[string]*funcScope{},
		typeInfo:  &pkg.Info,
	}

	// Resolve the entrypoint of the program
	main, mainFile := resolveEntryPoint(mainIdent, pkg)
	if main == nil {
		log.Fatal("could not find func main. did you forgot to declare it?")
	}

	funUsage := analyzeFuncUsage(info.program.AllPackages)

	// Bring all imported functions into scope
	for _, pkg := range info.program.AllPackages {
		for _, f := range pkg.Files {
			c.resolveFuncDecls(f)
		}
	}

	// convert the entry point first
	c.convertFuncDecl(mainFile, main)

	// Generate the code for the program
	for _, pkg := range info.program.AllPackages {
		c.typeInfo = &pkg.Info

		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				switch n := decl.(type) {
				case *ast.FuncDecl:
					// Dont convert the function if its not used. This will save alot
					// of bytecode space.
					if n.Name.Name != mainIdent && funUsage.funcUsed(n.Name.Name) {
						c.convertFuncDecl(f, n)
					}
				}
			}
		}
	}

	c.writeJumps()

	return c.prog, nil
}

func (c *codegen) resolveFuncDecls(f *ast.File) {
	for _, decl := range f.Decls {
		switch n := decl.(type) {
		case *ast.FuncDecl:
			if n.Name.Name != mainIdent {
				c.newFunc(n)
			}
		}
	}
}

func (c *codegen) writeJumps() {
	b := c.prog.Bytes()
	for i, op := range b {
		j := i + 1
		switch vm.Opcode(op) {
		case vm.Ojmpifnot, vm.Ojmpif, vm.Ocall:
			index := binary.LittleEndian.Uint16(b[j : j+2])
			if int(index) > len(c.l) {
				continue
			}
			offset := uint16(c.l[index] - i)
			if offset < 0 {
				log.Fatalf("new offset is negative, table list %v", c.l)
			}
			binary.LittleEndian.PutUint16(b[j:j+2], offset)
		}
	}
}

func isSyscall(name string) bool {
	_, ok := vm.Syscalls[name]
	return ok
}

var noRetSyscalls = []string{
	"Notify", "Log", "Put", "Register", "Delete",
	"SetVotes", "ContractDestroy", "MerkleRoot", "Hash",
	"PrevHash", "GetHeader",
}

// isNoRetSyscall checks if the syscall has a return value.
func isNoRetSyscall(name string) bool {
	for _, s := range noRetSyscalls {
		if s == name {
			return true
		}
	}
	return false
}
