package main
import ("testing";"os";"path/filepath")
func TestProjectUsesDRAProfile_OneLineContainerBlock(t *testing.T){
 d:=t.TempDir(); os.MkdirAll(filepath.Join(d,".cloche"),0o755)
 os.WriteFile(filepath.Join(d,".cloche","a.cloche"),[]byte("workflow \"a\" {\n  host {}\n  container { profile = \"dra\" }\n}\n"),0o644)
 if !projectUsesDRAProfile(d){t.Fatal("one-line container block missed")}
}
