package api

import (
	"strings"

	"github.com/ark-local-ai/ark/apps/agent/internal/convo"
	"github.com/ark-local-ai/ark/apps/agent/internal/zones"
)

// zoneFromProposal 把对话里的 kind=zone 提案落成 zones.json 的一条区。
//
// 两条规则：
//   - ZoneID 为空 = 新建；非空 = 修改，**空字段代表不改**（模型只填用户提到的字段）。
//   - 校验顺序与表单创建完全一致：名字/输入目录/目标表/路径授权，任何一条不过都不落盘。
func (s *Server) zoneFromProposal(p *convo.Proposal) (zones.Zone, error) {
	st := s.zonesStore()
	if st == nil {
		return zones.Zone{}, errText("作业区存储不可用（工作区未就绪）")
	}

	var z zones.Zone
	if id := strings.TrimSpace(p.ZoneID); id != "" {
		got, ok := st.Get(id)
		if !ok {
			return zones.Zone{}, zones.ErrNotFound
		}
		z = got
	}

	if v := strings.TrimSpace(p.ZoneName); v != "" {
		z.Name = v
	}
	if strings.TrimSpace(z.Name) == "" {
		return zones.Zone{}, errText("作业区需要名字")
	}

	if v := strings.TrimSpace(p.ZoneInputType); v != "" {
		z.Input.Type = v
	}
	if z.Input.Type == "" {
		z.Input.Type = zones.InputFolder
	}
	if v := strings.TrimSpace(p.ZoneInputPath); v != "" {
		z.Input.Path = v
	}
	if v := strings.TrimSpace(p.ZoneInputPattern); v != "" {
		z.Input.Pattern = v
	}
	if z.Input.Type == zones.InputFolder && strings.TrimSpace(z.Input.Path) == "" {
		return zones.Zone{}, errText("作业区需要输入目录（授权目录）")
	}

	if v := strings.TrimSpace(p.ZoneTargetFile); v != "" {
		z.Target.File = v
	}
	if v := strings.TrimSpace(p.ZoneTargetSheet); v != "" {
		z.Target.Sheet = v
	}
	if v := strings.TrimSpace(p.ZoneTargetNewTable); v != "" {
		z.Target.NewTable = v
	}

	if v := strings.TrimSpace(p.Mode); v != "" {
		z.Mode = v
	}
	if z.Mode == "" {
		z.Mode = zones.ModeDryRun
	}
	if z.Mode != zones.ModeDryRun && z.Mode != zones.ModeApply {
		return zones.Zone{}, errText("mode 只能是 dryRun 或 apply")
	}

	if err := s.validateZone(z); err != nil {
		return zones.Zone{}, err
	}
	return st.Put(z)
}
