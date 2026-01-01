package inject

import (
	"reflect"
	"strconv"
	"strings"

	"github.com/pkg/errors"
)

// unsafeDFSDetectCycle performs DFS to detect circular dependencies starting from a target typeKey
func (inj *Injector) unsafeDFSDetectCycle(targetKey typeKey) error {
	return inj.unsafeDFSDetectCycleRecursive(
		targetKey,
		make(map[typeKey]bool),
		[]directDep{
			{
				key: targetKey,
				src: "",
			},
		},
	)
}

func (inj *Injector) unsafeDFSDetectCycleRecursive(targetKey typeKey, visited map[typeKey]bool, path []directDep) error {
	if visited[targetKey] {
		// Found a cycle - find where the cycle starts
		cycleStart := -1
		for i, dep := range path {
			if dep.key == targetKey {
				cycleStart = i
				break
			}
		}
		if cycleStart >= 0 {
			// Extract the cycle portion
			cyclePart := path[cycleStart:]
			if len(cyclePart) > 0 {
				cycleStrings := make([]string, len(cyclePart))
				for i, dep := range cyclePart {
					cycleStrings[i] = dep.String()
				}
				return errors.Wrap(ErrCircularDependency, strings.Join(cycleStrings, " -> "))
			}
		}
	}

	visited[targetKey] = true
	defer func() {
		visited[targetKey] = false // Backtrack
	}()

	// Get all dependencies for this type (including co-constructor dependencies)
	dependencies, err := inj.unsafeGetDirectDependencies(targetKey)
	if err != nil {
		return err
	}

	// Recursively check each dependency
	for _, depInfo := range dependencies {
		if err := inj.unsafeDFSDetectCycleRecursive(depInfo.key, visited, append(path, depInfo)); err != nil {
			return err
		}
	}

	return nil
}

// directDep represents a dependency with its source information
type directDep struct {
	key typeKey
	src string // describes where this dependency comes from
}

// String returns a formatted representation of the dependency
func (d directDep) String() string {
	typeStr := d.key.rt.String()
	if d.key.index > 0 {
		typeStr = typeStr + "[" + strconv.Itoa(d.key.index) + "]"
	}
	if d.src == "" {
		return typeStr
	}
	return typeStr + "@" + d.src
}

// unsafeGetDirectDependencies returns all dependencies for a given typeKey, including co-constructor dependencies.
// A type's dependencies include:
// 1. Its struct field inject tag dependency types (highest priority)
// 2. Its provider.ctor's In types (except Context)
// 3. Co-constructor dependencies: dependencies of sibling types that could create indirect cycles
func (inj *Injector) unsafeGetDirectDependencies(targetKey typeKey) ([]directDep, error) {
	var deps []directDep
	seen := make(map[typeKey]bool) // Track seen keys

	addFieldDep := func(typ reflect.Type) error {
		fieldDeps, err := getStructFieldDependencies(typ)
		if err != nil {
			return err
		}
		for _, depType := range fieldDeps {
			// For field dependencies, use index 0 (normal types)
			depKey := typeKey{rt: depType, index: 0}
			if !seen[depKey] {
				deps = append(deps, directDep{
					key: depKey,
					src: typ.String(), // Direct field injection dependency
				})
				seen[depKey] = true
			}
		}
		return nil
	}

	// 1. Add struct field dependencies first (inject tag dependencies) - these have highest priority
	if err := addFieldDep(targetKey.rt); err != nil {
		return nil, err
	}

	// Check if this type has a provider
	if provider, ok := inj.providers[targetKey]; ok {
		providerType := reflect.TypeOf(provider.ctor)

		// 2. Add input dependencies (function parameters, except Context)
		for i := 0; i < providerType.NumIn(); i++ {
			inType := providerType.In(i)
			if inType == typeContext {
				continue
			}
			if !IsInputTypeAllowed(inType) {
				return nil, errors.Wrapf(ErrTypeNotAllowed, "input type %s is not allowed", inType.String())
			}

			// For Slice[T] dependencies, add all Element[T] providers as dependencies
			if innerType := getSliceInnerType(inType); innerType != nil {
				// Find the Element[T] type for this inner type
				for et, count := range inj.elementCounts {
					if inner := getElementInnerType(et); inner != nil && inner == innerType {
						for idx := 0; idx < count; idx++ {
							depKey := typeKey{rt: et, index: idx}
							if !seen[depKey] {
								deps = append(deps, directDep{
									key: depKey,
									src: "", // Slice dependency on Element
								})
								seen[depKey] = true
							}
						}
					}
				}
				continue
			}

			// For input dependencies, use index 0 (normal types)
			depKey := typeKey{rt: inType, index: 0}
			if !seen[depKey] {
				deps = append(deps, directDep{
					key: depKey,
					src: "", // Direct constructor parameter dependency
				})
				seen[depKey] = true
			}
		}

		// 3. Add co-constructor dependencies: dependencies from sibling types that have inject tags
		sameConstructorOutputs, _ := getValidOutputTypes(providerType)
		for _, siblingType := range sameConstructorOutputs {
			if siblingType != targetKey.rt {
				// Only add sibling type dependencies if the sibling has inject tag dependencies
				if err := addFieldDep(siblingType); err != nil {
					return nil, err
				}
			}
		}
	}

	return deps, nil
}

// getStructFieldDependencies extracts injection dependencies from struct fields
func getStructFieldDependencies(t reflect.Type) ([]reflect.Type, error) {
	var deps []reflect.Type
	seen := make(map[reflect.Type]bool) // Use map for deduplication

	// Dereference pointer types
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}

	if t.Kind() != reflect.Struct {
		return nil, nil
	}

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if _, ok := field.Tag.Lookup(TagName); ok {
			if !IsInputTypeAllowed(field.Type) {
				return nil, errors.Wrapf(ErrTypeNotAllowed, "field type %s is not allowed", field.Type.String())
			}
			if !seen[field.Type] {
				deps = append(deps, field.Type)
				seen[field.Type] = true
			}
		}
	}

	return deps, nil
}
