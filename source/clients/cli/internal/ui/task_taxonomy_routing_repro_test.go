package ui

import (
	"strings"
	"testing"
)

func TestTaskTaxonomyDedicatedRoutingPage(t *testing.T) {
	tab := configTabFromID("Routing")
	if tab < 0 || tab >= configTabCount {
		t.Fatalf("Routing page missing; tabs=%v", configTabLabels)
	}
	if tab == configTabCloud || tab == configTabModels {
		t.Fatal("Routing must own a separate page from Cloud and Local Models")
	}
}

func TestTaskTaxonomyCloudDoesNotOwnRoutingControls(t *testing.T) {
	sp := cloudSamplePage()
	for _, field := range sp.buildCloudSection().Fields {
		if strings.HasPrefix(field.Key(), "routing-") || strings.HasPrefix(field.Key(), "cloud-routing-") || strings.HasPrefix(field.Key(), "cloud-task-") {
			t.Errorf("Cloud still owns routing control %q", field.Key())
		}
	}
}
