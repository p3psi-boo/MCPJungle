package cmd

import (
	"testing"

	"github.com/mcpjungle/mcpjungle/pkg/testhelpers"
)

func TestGetCommandStructure(t *testing.T) {
	t.Run("command_properties", func(t *testing.T) {
		testhelpers.AssertEqual(t, "get", getCmd.Use)
		testhelpers.AssertEqual(t, "Get entities like Prompts and Groups", getCmd.Short)
	})

	t.Run("command_annotations", func(t *testing.T) {
		annotationTests := []testhelpers.CommandAnnotationTest{
			{Key: "group", Expected: string(subCommandGroupAdvanced)},
			{Key: "order", Expected: "1"},
		}
		testhelpers.TestCommandAnnotations(t, getCmd.Annotations, annotationTests)
	})
}

func TestGetGroupSubcommand(t *testing.T) {
	t.Run("command_properties", func(t *testing.T) {
		testhelpers.AssertEqual(t, "group [name]", getGroupCmd.Use)
		testhelpers.AssertEqual(t, "Get information about a specific Group", getGroupCmd.Short)
		testhelpers.AssertNotNil(t, getGroupCmd.Long)
		testhelpers.AssertTrue(t, len(getGroupCmd.Long) > 0, "Long description should not be empty")
	})

	t.Run("command_functions", func(t *testing.T) {
		testhelpers.AssertNotNil(t, getGroupCmd.RunE)
		testhelpers.AssertNotNil(t, getGroupCmd.Args)
	})

	t.Run("long_description_content", func(t *testing.T) {
		longDesc := getGroupCmd.Long
		expectedPhrases := []string{
			"Get information about a specific Group by name",
			"returns the configuration of the Group",
			"which tools, prompts, resources, and skills are included",
		}

		for _, phrase := range expectedPhrases {
			testhelpers.AssertTrue(t, testhelpers.Contains(longDesc, phrase),
				"Expected long description to contain: "+phrase)
		}
	})
}

func TestGetResourceSubcommand(t *testing.T) {
	testhelpers.AssertEqual(t, "resource [uri]", getResourceCmd.Use)
	testhelpers.AssertEqual(t, "Get resource metadata", getResourceCmd.Short)
	testhelpers.AssertNotNil(t, getResourceCmd.Long)
	testhelpers.AssertNotNil(t, getResourceCmd.RunE)
	testhelpers.AssertNotNil(t, getResourceCmd.Args)

	readFlag := getResourceCmd.Flags().Lookup("read")
	testhelpers.AssertNotNil(t, readFlag)
	testhelpers.AssertTrue(t, len(readFlag.Usage) > 0, "Read flag should have usage description")
}
