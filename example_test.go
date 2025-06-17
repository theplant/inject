package inject_test

import (
	"fmt"

	"github.com/theplant/inject"
)

// Define interfaces and implementations
type Printer interface {
	Print() string
}

type SimplePrinter struct{}

func (p *SimplePrinter) Print() string {
	return "Printing document"
}

// New type definition
type DocumentDescription string

type Document struct {
	Injector *inject.Injector `inject:""` // Injector will be provided by default, so you can also get it if needed

	ID          string              // Not injected
	Description DocumentDescription `inject:""`         // Exported non-optional field
	Printer     Printer             `inject:""`         // Exported non-optional field
	Size        int64               `inject:"optional"` // Exported optional field
	page        int                 `inject:""`         // Unexported non-optional field
	name        string              `inject:"optional"` // Unexported optional field
	ReadCount   int32               `inject:""`         // Unexported non-optional field
}

func ExampleInjector() {
	inj := inject.New()

	// Provide dependencies
	if err := inj.Provide(
		func() Printer {
			return &SimplePrinter{}
		},
		func() string {
			return "A simple string"
		},
		func() DocumentDescription {
			return "A document description"
		},
		func() (int, int32) {
			return 42, 32
		},
	); err != nil {
		panic(err)
	}

	{
		// Resolve dependencies
		var printer Printer
		if err := inj.Resolve(&printer); err != nil {
			panic(err)
		}
		fmt.Println("Resolved printer:", printer.Print())
	}

	printDoc := func(doc *Document) {
		fmt.Printf("Document id: %q\n", doc.ID)
		fmt.Printf("Document description: %q\n", doc.Description)
		fmt.Printf("Document printer: %q\n", doc.Printer.Print())
		fmt.Printf("Document size: %d\n", doc.Size)
		fmt.Printf("Document page: %d\n", doc.page)
		fmt.Printf("Document name: %q\n", doc.name)
		fmt.Printf("Document read count: %d\n", doc.ReadCount)
	}

	fmt.Println("-------")

	{
		// Invoke a function
		results, err := inj.Invoke(func(printer Printer) *Document {
			return &Document{
				// This value will be retained as it is not tagged with `inject`, despite string being provided
				ID: "idInvoked",
				// This value will be overridden since it is tagged with `inject` and DocumentDescription is provided
				Description: "DescriptionInvoked",
				// This value will be overridden with the same value since it is tagged with `inject` and Printer is provided
				Printer: printer,
				// This value will be retained since it is tagged with `inject:"optional"` and int64 is not provided
				Size: 100,
			}
		})
		if err != nil {
			panic(err)
		}

		printDoc(results[0].(*Document))
	}

	fmt.Println("-------")

	{
		// Apply dependencies to a struct instance
		doc := &Document{}
		if err := inj.Apply(doc); err != nil {
			panic(err)
		}

		printDoc(doc)
	}

	fmt.Println("-------")

	{
		// Create a child injector and then apply dependencies to a struct instance
		child := inject.New()
		_ = child.SetParent(inj)

		doc := &Document{}
		if err := child.Apply(doc); err != nil {
			panic(err)
		}

		printDoc(doc)
	}

	// Output:
	// Resolved printer: Printing document
	// -------
	// Document id: "idInvoked"
	// Document description: "A document description"
	// Document printer: "Printing document"
	// Document size: 100
	// Document page: 42
	// Document name: "A simple string"
	// Document read count: 32
	// -------
	// Document id: ""
	// Document description: "A document description"
	// Document printer: "Printing document"
	// Document size: 0
	// Document page: 42
	// Document name: "A simple string"
	// Document read count: 32
	// -------
	// Document id: ""
	// Document description: "A document description"
	// Document printer: "Printing document"
	// Document size: 0
	// Document page: 42
	// Document name: "A simple string"
	// Document read count: 32
}
