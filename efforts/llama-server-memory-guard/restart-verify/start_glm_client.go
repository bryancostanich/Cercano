package main
import (
  "context"
  "fmt"
  "strings"
  "time"
  "cercano/source/server/pkg/agentclient"
)
func main() {
  ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
  defer cancel()
  c, err := agentclient.Dial(ctx, "localhost:50052")
  if err != nil { panic(err) }
  defer c.Close()
  cat, err := c.ListRuntimeModels(ctx)
  if err != nil { panic(err) }
  var id string
  for _, m := range cat.Models {
    if strings.Contains(strings.ToLower(m.DisplayName), "glm-4.5-air") || strings.Contains(strings.ToLower(m.ID), "glm-4.5-air") {
      id = m.ID
      fmt.Printf("glm_model_id=%s display=%q state=%s\n", m.ID, m.DisplayName, m.RuntimeState)
      break
    }
  }
  if id == "" { panic("GLM model not found") }
  inst, err := c.StartRuntimeModel(ctx, "llama_server", id)
  if err != nil { panic(err) }
  fmt.Printf("start_ok instance_id=%s pid=%d port=%d state=%s\n", inst.ID, inst.PID, inst.Port, inst.State)
}
