package aclsync

import "fmt"

// Tuple is an OpenFGA relationship tuple (user/userset, relation, object). It is
// comparable so it can be used as a map key for set diffing.
type Tuple struct {
	User     string // "user:finance_user" or "group:finance#member" or "folder:finance-folder"
	Relation string // "member" | "viewer" | "parent"
	Object   string // "group:finance" | "folder:finance-folder" | "document:security-policy"
}

func userRef(id string) string         { return "user:" + id }
func groupRef(id string) string        { return "group:" + id }
func groupMembersRef(id string) string { return "group:" + id + "#member" }
func folderRef(id string) string       { return "folder:" + id }
func documentRef(id string) string     { return "document:" + id }

// PermissionSetToTuples converts a source-of-truth snapshot into the OpenFGA tuples that
// represent it under the Groundwork authorization model. Pure function — no I/O.
//
//	user:U          member group:G            (direct group membership)
//	group:H#member  member group:G            (nested group: H's members are members of G)
//	user:U          viewer folder:F           (direct folder viewer)
//	group:G#member  viewer folder:F           (group folder viewer)
//	folder:F        parent document:D         (document inherits folder F's viewers)
//	user:U          viewer document:D         (direct document viewer)
//	group:G#member  viewer document:D         (group document viewer)
func PermissionSetToTuples(ps PermissionSet) []Tuple {
	var tuples []Tuple

	for _, g := range ps.Groups {
		for _, u := range g.MemberUsers {
			tuples = append(tuples, Tuple{userRef(u), "member", groupRef(g.ID)})
		}
		for _, sub := range g.MemberGroups {
			tuples = append(tuples, Tuple{groupMembersRef(sub), "member", groupRef(g.ID)})
		}
	}

	for _, f := range ps.Folders {
		for _, u := range f.ViewerUsers {
			tuples = append(tuples, Tuple{userRef(u), "viewer", folderRef(f.ID)})
		}
		for _, gr := range f.ViewerGroups {
			tuples = append(tuples, Tuple{groupMembersRef(gr), "viewer", folderRef(f.ID)})
		}
	}

	for _, d := range ps.Documents {
		if d.FolderID != "" {
			tuples = append(tuples, Tuple{folderRef(d.FolderID), "parent", documentRef(d.ID)})
		}
		for _, u := range d.ViewerUsers {
			tuples = append(tuples, Tuple{userRef(u), "viewer", documentRef(d.ID)})
		}
		for _, gr := range d.ViewerGroups {
			tuples = append(tuples, Tuple{groupMembersRef(gr), "viewer", documentRef(d.ID)})
		}
	}

	return dedupeTuples(tuples)
}

func dedupeTuples(in []Tuple) []Tuple {
	seen := make(map[Tuple]bool, len(in))
	out := make([]Tuple, 0, len(in))
	for _, t := range in {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

func (t Tuple) String() string {
	return fmt.Sprintf("%s %s %s", t.User, t.Relation, t.Object)
}
