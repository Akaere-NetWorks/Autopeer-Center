package model

import (
	"reflect"
	"strings"
	"testing"
)

func TestPeerBunTags(t *testing.T) {
	typeOfPeer := reflect.TypeOf(Peer{})

	wgField, ok := typeOfPeer.FieldByName("WgManaged")
	if !ok {
		t.Fatal("WgManaged field not found")
	}
	if tag := wgField.Tag.Get("bun"); !strings.Contains(tag, "default:true") {
		t.Fatalf("WgManaged bun tag = %q, want default:true", tag)
	}

	createdField, ok := typeOfPeer.FieldByName("CreatedAt")
	if !ok {
		t.Fatal("CreatedAt field not found")
	}
	createdTag := createdField.Tag.Get("bun")
	for _, want := range []string{"nullzero", "default:now()"} {
		if !strings.Contains(createdTag, want) {
			t.Fatalf("CreatedAt bun tag = %q, want %q", createdTag, want)
		}
	}

	updatedField, ok := typeOfPeer.FieldByName("UpdatedAt")
	if !ok {
		t.Fatal("UpdatedAt field not found")
	}
	updatedTag := updatedField.Tag.Get("bun")
	for _, want := range []string{"nullzero", "default:now()"} {
		if !strings.Contains(updatedTag, want) {
			t.Fatalf("UpdatedAt bun tag = %q, want %q", updatedTag, want)
		}
	}
}
