package store

import (
	"testing"
)

func TestField(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		// Test unset field
		var field Field[string]
		if field.IsSet() {
			t.Error("New field should not be set")
		}

		if field.Get() != "" {
			t.Error("Unset field should return zero value")
		}

		if field.Or("default") != "default" {
			t.Error("Or() should return default for unset field")
		}

		// Test set field
		field.Set("value")
		if !field.IsSet() {
			t.Error("Field should be set after Set()")
		}

		if field.Get() != "value" {
			t.Error("Get() should return set value")
		}

		if field.Or("default") != "value" {
			t.Error("Or() should return set value even when default provided")
		}

		// Test unset
		field.Unset()
		if field.IsSet() {
			t.Error("Field should not be set after Unset()")
		}

		// Test SetDefault
		field.SetDefault("default")
		if !field.IsSet() {
			t.Error("Field should be set after SetDefault()")
		}

		if field.Get() != "default" {
			t.Error("Get() should return default value after SetDefault()")
		}

		// Default shouldn't overwrite existing value
		field.Set("value")
		field.SetDefault("another")
		if field.Get() != "value" {
			t.Error("SetDefault() should not override existing value")
		}

		// Test SetDefaultFrom
		field.Unset()
		field.SetDefaultFrom(func() string { return "from-func" })
		if field.Get() != "from-func" {
			t.Error("SetDefaultFrom() should set value from function")
		}

		// Function shouldn't be called if already set
		field.Set("direct")
		called := false
		field.SetDefaultFrom(func() string {
			called = true
			return "should-not-set"
		})

		if called {
			t.Error("SetDefaultFrom function should not be called when field is already set")
		}

		if field.Get() != "direct" {
			t.Error("SetDefaultFrom() should not change existing value")
		}

		// Test Match
		matched := false
		field.Match("direct", func() { matched = true })
		if !matched {
			t.Error("Match() should call function when value matches")
		}

		matched = false
		field.Match("wrong", func() { matched = true })
		if matched {
			t.Error("Match() should not call function when value doesn't match")
		}
	})
}

func TestMapField(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		var field MapField[string, int]

		// New field should be unset
		if field.IsSet() {
			t.Error("New map field should not be set")
		}

		// Set a key
		field.SetKey("one", 1)
		if !field.IsSet() {
			t.Error("Field should be set after SetKey()")
		}

		if field.Get()["one"] != 1 {
			t.Error("Get() should return map with key set")
		}

		// Set multiple keys
		field.SetKey("two", 2)
		if len(field.Get()) != 2 || field.Get()["two"] != 2 {
			t.Error("Multiple keys should be set correctly")
		}

		// Test constructor
		m := map[string]int{"a": 1, "b": 2}
		mapField := ToMapField(m)

		if !mapField.IsSet() || len(mapField.Get()) != 2 {
			t.Error("ToMapField() should create set field with correct map")
		}

		// Test clone
		cloned := mapField.Cloned()
		if len(cloned) != 2 || cloned["a"] != 1 || cloned["b"] != 2 {
			t.Error("Cloned() should return copy of the map")
		}

		// Modifying clone should not affect original
		cloned["c"] = 3
		if len(mapField.Get()) != 2 || mapField.Get()["c"] != 0 {
			t.Error("Modifying cloned map should not affect original")
		}

		// Test nil map
		var nilField MapField[string, int]
		nilClone := nilField.Cloned()
		if nilClone != nil {
			t.Error("Cloning nil map field should return nil")
		}
	})
}

func TestEither(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		var either Either[bool, string]

		// New either should be unset
		if either.IsSet() || either.IsLeft() || either.IsRight() {
			t.Error("New either should not be set")
		}

		// Test left value
		either.SetLeft(true)
		if !either.IsSet() || !either.IsLeft() || either.IsRight() {
			t.Error("Either should be set and left after SetLeft()")
		}

		if either.Left() != true {
			t.Error("Left() should return set value")
		}

		// Right should be zero value
		if either.Right() != "" {
			t.Error("Right() should return zero when left is set")
		}

		// SetRight should clear left
		either.SetRight("value")
		if !either.IsSet() || either.IsLeft() || !either.IsRight() {
			t.Error("Either should be set and right after SetRight()")
		}

		if either.Right() != "value" {
			t.Error("Right() should return set value")
		}

		// Left should be zero value
		if either.Left() != false {
			t.Error("Left() should return zero when right is set")
		}

		// Test Match
		leftCalled := false
		rightCalled := false
		either.Match(
			func(b bool) { leftCalled = true },
			func(s string) { rightCalled = true },
		)

		if leftCalled || !rightCalled {
			t.Error("Match() should call right function when right is set")
		}

		// Switch to left
		either.SetLeft(true)
		leftCalled = false
		rightCalled = false
		either.Match(
			func(b bool) { leftCalled = true },
			func(s string) { rightCalled = true },
		)

		if !leftCalled || rightCalled {
			t.Error("Match() should call left function when left is set")
		}

		// Test OrLeft
		if either.OrLeft(false) != true {
			t.Error("OrLeft() should return left value when set")
		}

		// Test OrRight
		either.SetRight("value")
		if either.OrRight("default") != "value" {
			t.Error("OrRight() should return right value when set")
		}

		// Test UnsetLeft/UnsetRight
		either.UnsetRight()
		if either.IsRight() {
			t.Error("UnsetRight() should clear right value")
		}

		// Test Get
		either.SetLeft(true)
		left, right := either.Get()
		if left != true || right != "" {
			t.Error("Get() should return correct values")
		}

		either.SetRight("value")
		left, right = either.Get()
		if left != false || right != "value" {
			t.Error("Get() should return correct values after switch")
		}
	})

	t.Run("Default", func(t *testing.T) {
		var either Either[bool, string]

		// Test SetDefaultLeft
		either.SetDefaultLeft(true)
		if !either.IsLeft() || either.Left() != true {
			t.Error("SetDefaultLeft() should set left when unset")
		}

		// Test SetDefaultRight
		either = Either[bool, string]{}
		either.SetDefaultRight("default")
		if !either.IsRight() || either.Right() != "default" {
			t.Error("SetDefaultRight() should set right when unset")
		}

		// Test SetDefaultFrom
		either = Either[bool, string]{}
		either.SetDefaultFrom(func() (bool, string) { return true, "from-func" })
		if !either.IsSet() {
			t.Error("SetDefaultFrom() should set either")
		}

		// Function shouldn't be called if already set
		either.SetLeft(false)
		called := false
		either.SetDefaultFrom(func() (bool, string) {
			called = true
			return true, "should-not-set"
		})

		if called {
			t.Error("SetDefaultFrom function should not be called when either is already set")
		}

		if either.Left() != false {
			t.Error("SetDefaultFrom() should not change existing value")
		}
	})
}

func TestSliceField(t *testing.T) {
	t.Run("Basic", func(t *testing.T) {
		// Create a slice field
		sliceField := ToSliceField([]int{1, 2, 3})

		if !sliceField.IsSet() {
			t.Error("SliceField should be set after creation")
		}

		if len(sliceField.Get()) != 3 || sliceField.Get()[0] != 1 {
			t.Error("Get() should return correct slice")
		}

		// Test clone
		cloned := sliceField.Cloned()
		if len(cloned) != 3 || cloned[0] != 1 {
			t.Error("Cloned() should return copy of slice")
		}

		// Modifying clone should not affect original
		cloned[0] = 99
		if sliceField.Get()[0] != 1 {
			t.Error("Modifying cloned slice should not affect original")
		}

		// Test empty slice
		var emptyField SliceField[int]
		emptyClone := emptyField.Cloned()
		if emptyClone != nil {
			t.Error("Cloning unset slice field should return nil")
		}
	})
}
