package webui

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/qimaoww/qcontrolhub/internal/configschema"
	"github.com/qimaoww/qcontrolhub/internal/core"
	"github.com/qimaoww/qcontrolhub/internal/serverconfig"
	"github.com/qimaoww/qcontrolhub/internal/store"
)

type agentConfigFieldView struct {
	configschema.Field
	Present  bool
	Selected bool
}

type agentConfigPageData struct {
	Agent              core.Agent
	Config             core.Config
	HasSavedConfig     bool
	Catalog            configschema.Catalog
	Fields             []agentConfigFieldView
	Selected           configschema.Field
	Fragment           string
	FieldPresent       bool
	ReturnTo           string
	Server             serverBuilderData
	Revisions          []core.Config
	RevisionPreview    core.Config
	HasRevisionPreview bool
	SourceDraft        bool
	SourceTaskID       string
}

type serverBuilderData struct {
	Protocols     []serverconfig.Protocol
	Selected      serverconfig.Protocol
	Input         serverconfig.Input
	PortValue     string
	RealityPreset []string
	Editing       bool
	Mutation      string
	OriginalTag   string
	Inbounds      []serverconfig.Input
}

type agentConfigRenderOptions struct {
	SelectedKey       string
	RequestedProtocol string
	RequestedInbound  string
	Regenerate        bool
	Notice            string
	Error             string
	FocusTaskID       string
	Server            *serverBuilderData
}

func (s *Server) agentConfigPage(w http.ResponseWriter, request *http.Request) {
	current, ok := s.currentSession(request)
	if !ok {
		http.Redirect(w, request, "/login", http.StatusSeeOther)
		return
	}
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		http.NotFound(w, request)
		return
	}
	s.renderAgentConfig(w, request, current, request.PathValue("id"), engine, agentConfigRenderOptions{
		SelectedKey:       request.URL.Query().Get("key"),
		RequestedProtocol: request.URL.Query().Get("protocol"),
		RequestedInbound:  request.URL.Query().Get("inbound"),
		Regenerate:        request.URL.Query().Get("regenerate") == "1",
		Notice:            request.URL.Query().Get("notice"),
		Error:             request.URL.Query().Get("error"),
		FocusTaskID:       request.URL.Query().Get("task"),
	})
}

func (s *Server) renderAgentConfig(w http.ResponseWriter, request *http.Request, current session, agentID string, engine core.Engine, options agentConfigRenderOptions) {
	agent, err := s.store.GetAgent(request.Context(), agentID)
	if errors.Is(err, store.ErrNotFound) {
		http.NotFound(w, request)
		return
	}
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	if !agentSupports(agent, engine) {
		http.NotFound(w, request)
		return
	}
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		http.NotFound(w, request)
		return
	}
	config, err := s.store.AgentConfig(request.Context(), agent.ID, engine)
	hasSavedConfig := err == nil
	if errors.Is(err, store.ErrNotFound) {
		config = defaultAgentConfig(agent, engine)
	} else if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	pageError := options.Error
	sourceTaskID := strings.TrimSpace(request.URL.Query().Get("source_task"))
	sourceDraft := false
	if sourceTaskID != "" {
		sourceContent, sourceErr := s.store.ReadTaskConfigSnapshot(request.Context(), sourceTaskID, agent.ID, engine)
		if errors.Is(sourceErr, store.ErrNotFound) {
			pageError = firstNonEmpty(pageError, "当前配置尚未读取完成，或已被更新的读取结果替代")
		} else if sourceErr != nil {
			s.renderDatabaseError(w, sourceErr)
			return
		} else {
			config.Content = sourceContent
			sourceDraft = true
		}
	}
	keys, err := configschema.RootKeys(engine, config.Content)
	if err != nil {
		http.Error(w, "存储的配置无法解析", http.StatusInternalServerError)
		return
	}
	selected := selectCatalogField(catalog, options.SelectedKey)
	fragment, present, err := configschema.Fragment(engine, config.Content, selected.Key)
	if err != nil {
		http.Error(w, "配置片段无法解析", http.StatusInternalServerError)
		return
	}
	fields := make([]agentConfigFieldView, 0, len(catalog.Fields))
	for _, item := range catalog.Fields {
		fields = append(fields, agentConfigFieldView{Field: item, Present: keys[item.Key], Selected: item.Key == selected.Key})
	}
	overview, err := s.store.Overview(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	settings, err := s.panelSettings(request.Context())
	if err != nil {
		s.renderDatabaseError(w, err)
		return
	}
	returnTo := agentConfigReturnTo(request)
	var serverBuilder serverBuilderData
	if options.Server != nil {
		serverBuilder = *options.Server
	} else {
		serverBuilder, err = newServerBuilder(engine, config.Content, options.RequestedProtocol, options.RequestedInbound, options.Regenerate)
		if err != nil {
			http.Error(w, "无法生成安全凭据", http.StatusInternalServerError)
			return
		}
	}
	var revisions []core.Config
	var revisionPreview core.Config
	hasRevisionPreview := false
	if hasSavedConfig {
		revisions, err = s.store.ListConfigRevisions(request.Context(), config.ID, 50)
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
		if rawRevision := request.URL.Query().Get("revision"); rawRevision != "" {
			version, parseErr := strconv.Atoi(rawRevision)
			if parseErr != nil || version < 1 {
				pageError = firstNonEmpty(pageError, "修订号无效")
			} else {
				revisionPreview, err = s.store.ConfigRevision(request.Context(), config.ID, version)
				if err != nil {
					pageError = firstNonEmpty(pageError, "找不到指定配置修订")
				} else {
					hasRevisionPreview = true
				}
			}
		}
	}
	data := pageData{
		Title: "预选配置 · " + agent.Name, Active: "agent-config", CSRF: current.CSRF,
		Notice: options.Notice, Error: pageError, Overview: overview, FocusTaskID: options.FocusTaskID, Settings: settings,
		AgentConfigPage: &agentConfigPageData{
			Agent: agent, Config: config, HasSavedConfig: hasSavedConfig, Catalog: catalog,
			Fields: fields, Selected: selected, Fragment: fragment, FieldPresent: present, ReturnTo: returnTo, Server: serverBuilder,
			Revisions: revisions, RevisionPreview: revisionPreview, HasRevisionPreview: hasRevisionPreview,
			SourceDraft: sourceDraft, SourceTaskID: sourceTaskID,
		},
	}
	s.renderTemplate(w, "app", data)
}

func (s *Server) saveAgentConfig(w http.ResponseWriter, request *http.Request) {
	currentSession, ok := s.requireRoleWithLimit(w, request, core.RoleOperator, core.MaxConfigEnvelopeBytes)
	if !ok {
		return
	}
	engine, err := core.ParseEngine(request.PathValue("engine"))
	if err != nil {
		http.NotFound(w, request)
		return
	}
	agentID := request.PathValue("id")
	catalog, err := configschema.CatalogFor(engine)
	if err != nil {
		http.NotFound(w, request)
		return
	}
	mode := request.PostFormValue("mode")
	selected, fieldOK := findCatalogField(catalog, request.PostFormValue("key"))
	if mode == "field" && !fieldOK {
		http.Error(w, "unknown configuration key", http.StatusBadRequest)
		return
	}
	destination := "/agents/" + url.PathEscape(agentID) + "/config/" + url.PathEscape(string(engine))
	var submittedServer *serverBuilderData
	if mode == "server" {
		protocol := request.PostFormValue("protocol")
		selectedProtocol, found := serverconfig.FindProtocol(engine, protocol)
		if !found {
			http.Error(w, "unknown server inbound protocol", http.StatusBadRequest)
			return
		}
		builder := submittedServerBuilder(request, engine, selectedProtocol)
		submittedServer = &builder
		destination += "?protocol=" + url.QueryEscape(protocol)
		if builder.Mutation != "delete" && builder.Input.Tag != "" {
			destination = addQuery(destination, "inbound", builder.Input.Tag)
		}
	} else if mode == "field" {
		destination = agentConfigURL(agentID, engine, selected.Key)
	} else if mode == "source" {
		if requested := safeReturnTo(request.PostFormValue("return_to")); strings.HasPrefix(requested, "/configs?") {
			destination = requested
		}
	}
	renderServerError := func(message string) {
		if submittedServer == nil {
			redirectError(w, request, destination, message)
			return
		}
		s.renderAgentConfig(w, request, currentSession, agentID, engine, agentConfigRenderOptions{
			RequestedProtocol: submittedServer.Selected.Key,
			Error:             message,
			Server:            submittedServer,
		})
	}
	expectedVersion, err := strconv.Atoi(request.PostFormValue("version"))
	if err != nil || expectedVersion < 0 {
		renderServerError("配置版本无效，请刷新页面后重试")
		return
	}
	sourceTaskID := strings.TrimSpace(request.PostFormValue("source_task"))
	sourceContent := ""
	if sourceTaskID != "" && mode != "source" {
		sourceContent, err = s.store.ReadTaskConfigSnapshot(request.Context(), sourceTaskID, agentID, engine)
		if errors.Is(err, store.ErrNotFound) {
			renderServerError("当前配置尚未读取完成，或已被更新的读取结果替代；请重新打开页面")
			return
		}
		if err != nil {
			s.renderDatabaseError(w, err)
			return
		}
	}
	intent := request.PostFormValue("intent")
	if intent != "validate" && intent != "deploy" {
		http.Error(w, "unsupported save action", http.StatusBadRequest)
		return
	}
	content := request.PostFormValue("content")
	if mode == "server" {
		port, portErr := strconv.Atoi(submittedServer.PortValue)
		if portErr != nil {
			renderServerError("监听端口格式无效")
			return
		}
		serverInput := submittedServer.Input
		serverInput.Port = port
		mutation := submittedServer.Mutation
		if mutation != "add" && mutation != "modify" && mutation != "delete" {
			renderServerError("请选择新增、修改或删除操作")
			return
		}
		if serverInput.RealityEnabled && mutation != "delete" {
			target, probeErr := serverconfig.ProbeRealityTarget(request.Context(), serverInput.RealityServerName)
			if probeErr != nil {
				renderServerError(probeErr.Error())
				return
			}
			serverInput.RealityServerName = target.ServerName
		}
		content, err = serverconfig.Generate(engine, serverInput)
		if err != nil {
			renderServerError(err.Error())
			return
		}
		if expectedVersion > 0 || sourceContent != "" {
			current := core.Config{Content: sourceContent}
			if sourceContent == "" {
				var loadErr error
				current, loadErr = s.store.AgentConfig(request.Context(), agentID, engine)
				if loadErr != nil {
					renderServerError("读取当前配置失败：" + loadErr.Error())
					return
				}
			}
			content, err = serverconfig.MutateGenerated(engine, current.Content, content, submittedServer.OriginalTag, mutation)
			if err != nil {
				renderServerError(err.Error())
				return
			}
		} else if mutation != "add" {
			renderServerError("当前没有可修改或删除的入站，请选择新增")
			return
		}
	} else if mode == "field" {
		current, loadErr := s.store.AgentConfig(request.Context(), agentID, engine)
		if errors.Is(loadErr, store.ErrNotFound) {
			agent, agentErr := s.store.GetAgent(request.Context(), agentID)
			if agentErr != nil || !agentSupports(agent, engine) {
				redirectError(w, request, "/agents", "节点不存在或不支持该内核")
				return
			}
			current = defaultAgentConfig(agent, engine)
		} else if loadErr != nil {
			s.renderDatabaseError(w, loadErr)
			return
		}
		if sourceContent != "" {
			current.Content = sourceContent
		}
		fieldMutation := request.PostFormValue("mutation")
		keys, keysErr := configschema.RootKeys(engine, current.Content)
		if keysErr != nil {
			redirectError(w, request, destination, "读取当前字段失败："+keysErr.Error())
			return
		}
		present := keys[selected.Key]
		switch fieldMutation {
		case "add":
			if present {
				redirectError(w, request, destination, "字段已存在，不能重复新增；请选择修改")
				return
			}
		case "modify", "delete":
			if !present {
				redirectError(w, request, destination, "字段不存在，不能修改或删除；请选择新增")
				return
			}
		default:
			redirectError(w, request, destination, "请选择新增、修改或删除操作")
			return
		}
		content, err = configschema.MergeFragment(engine, current.Content, selected.Key, request.PostFormValue("fragment"), fieldMutation == "delete")
		if err != nil {
			redirectError(w, request, destination, "片段格式无效："+err.Error())
			return
		}
	} else if mode != "source" {
		redirectError(w, request, destination, "不支持的编辑模式")
		return
	}
	candidate := core.Config{
		AgentID: agentID, Name: strings.TrimSpace(request.PostFormValue("name")),
		Description: strings.TrimSpace(request.PostFormValue("description")), Engine: engine, Content: content,
	}
	var saved core.Config
	if mode == "source" {
		current, currentErr := s.store.AgentConfig(request.Context(), agentID, engine)
		if currentErr != nil && !errors.Is(currentErr, store.ErrNotFound) {
			s.renderDatabaseError(w, currentErr)
			return
		}
		if currentErr == nil && current.Version == expectedVersion && current.Name == candidate.Name && current.Description == candidate.Description && current.Content == candidate.Content {
			saved = current
		}
	}
	if saved.ID == "" {
		saved, err = s.store.SaveAgentConfig(request.Context(), candidate, expectedVersion)
	}
	if err != nil {
		renderServerError(err.Error())
		return
	}
	s.recordAudit(request, "agent_config.saved", saved.ID, string(engine)+" v"+strconv.Itoa(saved.Version)+" "+agentID)
	if strings.HasPrefix(destination, "/configs?") {
		destination = addQuery(destination, "draft_version", strconv.Itoa(saved.Version))
	}
	if intent == "validate" || intent == "deploy" {
		action := core.ActionValidate
		if intent == "deploy" {
			action = core.ActionDeploy
		}
		task, taskErr := s.store.CreateTask(request.Context(), core.TaskRequest{
			AgentID: agentID, Action: action, Engine: engine, ConfigID: saved.ID,
		})
		if taskErr != nil {
			redirectError(w, request, destination, "配置已保存，但无法创建任务："+taskErr.Error())
			return
		}
		notice := "配置已保存，正在校验"
		if action == core.ActionDeploy {
			notice = "配置已保存，正在部署"
		}
		redirectNotice(w, request, addQuery(destination, "task", task.ID), notice)
		return
	}
	redirectNotice(w, request, destination, "节点配置已保存为 v"+strconv.Itoa(saved.Version))
}

func newServerBuilder(engine core.Engine, content, requestedProtocol, requestedInbound string, regenerate bool) (serverBuilderData, error) {
	protocols := serverconfig.Protocols(engine)
	presets := serverconfig.RealityServerNamePresets()
	inbounds := serverconfig.ParseAll(engine, content)
	var parsed serverconfig.Input
	parsedOK := false
	requestedInboundFound := false
	if requestedInbound != "" {
		for _, inbound := range inbounds {
			if inbound.Tag == requestedInbound {
				parsed, parsedOK = inbound, true
				requestedInboundFound = true
				break
			}
		}
	}
	if !parsedOK && len(inbounds) > 0 {
		parsed, parsedOK = inbounds[0], true
	}
	if requestedProtocol == "" && parsedOK {
		requestedProtocol = parsed.Protocol
	}
	selected, ok := serverconfig.FindProtocol(engine, requestedProtocol)
	if !ok {
		selected = protocols[0]
	}
	if !regenerate && parsedOK && parsed.Protocol == selected.Key {
		return serverBuilderData{Protocols: protocols, Selected: selected, Input: parsed, PortValue: strconv.Itoa(parsed.Port), RealityPreset: presets, Editing: true, Mutation: "modify", OriginalTag: parsed.Tag, Inbounds: inbounds}, nil
	}
	input, err := serverconfig.NewPlan(selected)
	if err != nil {
		return serverBuilderData{}, err
	}
	if regenerate && requestedInboundFound && parsed.Protocol == selected.Key {
		return serverBuilderData{
			Protocols: protocols, Selected: selected, Input: input, PortValue: strconv.Itoa(input.Port),
			RealityPreset: presets, Editing: true, Mutation: "modify",
			OriginalTag: parsed.Tag, Inbounds: inbounds,
		}, nil
	}
	return serverBuilderData{Protocols: protocols, Selected: selected, Input: input, PortValue: strconv.Itoa(input.Port), RealityPreset: presets, Mutation: "add", Inbounds: inbounds}, nil
}

func submittedServerBuilder(request *http.Request, engine core.Engine, selected serverconfig.Protocol) serverBuilderData {
	portValue := strings.TrimSpace(request.PostFormValue("port"))
	port, _ := strconv.Atoi(portValue)
	expectedVersion, _ := strconv.Atoi(request.PostFormValue("version"))
	return serverBuilderData{
		Protocols: serverconfig.Protocols(engine),
		Selected:  selected,
		Input: serverconfig.Input{
			Protocol: selected.Key, Tag: strings.TrimSpace(request.PostFormValue("tag")),
			Listen: strings.TrimSpace(request.PostFormValue("listen")), Port: port,
			Username: strings.TrimSpace(request.PostFormValue("username")), Credential: strings.TrimSpace(request.PostFormValue("credential")),
			SecondaryCredential: strings.TrimSpace(request.PostFormValue("secondary_credential")),
			Method:              request.PostFormValue("method"), Flow: request.PostFormValue("flow"), Transport: request.PostFormValue("transport"),
			TransportPath: strings.TrimSpace(request.PostFormValue("transport_path")), TLSEnabled: request.PostFormValue("tls_enabled") == "1",
			CertificatePath: strings.TrimSpace(request.PostFormValue("certificate_path")), PrivateKeyPath: strings.TrimSpace(request.PostFormValue("private_key_path")),
			RealityEnabled: request.PostFormValue("reality_enabled") == "1", RealityPrivateKey: request.PostFormValue("reality_private_key"),
			RealityPublicKey: request.PostFormValue("reality_public_key"), RealityShortID: request.PostFormValue("reality_short_id"),
			RealityServerName: strings.TrimSpace(request.PostFormValue("reality_server_name")),
		},
		PortValue:     portValue,
		RealityPreset: serverconfig.RealityServerNamePresets(),
		Editing:       expectedVersion > 0,
		Mutation:      strings.TrimSpace(request.PostFormValue("mutation")),
		OriginalTag:   strings.TrimSpace(request.PostFormValue("original_tag")),
	}
}

func findCatalogField(catalog configschema.Catalog, key string) (configschema.Field, bool) {
	for _, item := range catalog.Fields {
		if item.Key == key {
			return item, true
		}
	}
	return configschema.Field{}, false
}

func selectCatalogField(catalog configschema.Catalog, requested string) configschema.Field {
	if selected, ok := findCatalogField(catalog, requested); ok {
		return selected
	}
	preferred := map[core.Engine]string{
		core.EngineMihomo: "mixed-port", core.EngineXray: "inbounds", core.EngineSingBox: "inbounds", core.EngineShadowsocksRust: "server",
	}[catalog.Engine]
	for _, item := range catalog.Fields {
		if item.Key == preferred {
			return item
		}
	}
	return catalog.Fields[0]
}

func defaultAgentConfig(agent core.Agent, engine core.Engine) core.Config {
	content := ""
	switch engine {
	case core.EngineMihomo:
		content = "mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n"
	case core.EngineXray:
		content = "{\n  \"log\": {\"loglevel\": \"warning\"},\n  \"inbounds\": [],\n  \"outbounds\": []\n}\n"
	case core.EngineSingBox:
		content = "{\n  \"$schema\": \"https://sing-box.sagernet.org/schema.json\",\n  \"log\": {\"level\": \"info\"},\n  \"inbounds\": [],\n  \"outbounds\": []\n}\n"
	case core.EngineShadowsocksRust:
		content = "{\n  \"server\": \"127.0.0.1\",\n  \"server_port\": 8388,\n  \"password\": \"change-this-password\",\n  \"method\": \"chacha20-ietf-poly1305\",\n  \"mode\": \"tcp_and_udp\",\n  \"timeout\": 300,\n  \"no_delay\": true\n}\n"
	}
	return core.Config{
		AgentID: agent.ID, Name: agent.Name + " · " + engineName(engine),
		Description: "由节点配置页维护", Engine: engine, Content: content,
	}
}

func agentSupports(agent core.Agent, engine core.Engine) bool {
	for _, capability := range agent.Capabilities {
		if capability == engine {
			return true
		}
	}
	return false
}

func agentConfigURL(agentID string, engine core.Engine, key string) string {
	return "/agents/" + url.PathEscape(agentID) + "/config/" + url.PathEscape(string(engine)) + "?key=" + url.QueryEscape(key) + "#advanced"
}

func agentConfigReturnTo(request *http.Request) string {
	query := request.URL.Query()
	for _, key := range []string{"notice", "error", "task", "source_task", "client_host", "client_sni"} {
		query.Del(key)
	}
	destination := request.URL.EscapedPath()
	if encoded := query.Encode(); encoded != "" {
		destination += "?" + encoded
	}
	return destination
}
