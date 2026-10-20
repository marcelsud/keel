function renderGuide(components) {
  const $ = selector => document.querySelector(selector);
  const make = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };
  function codePanel(example, language) {
    const panel = make("div", "code-panel");
    panel.dataset.language = language;
    panel.dataset.mode = example.mode || "statements";
    panel.dataset.imports = JSON.stringify(example.imports || []);
    const bar = make("div", "code-bar");
    bar.append(make("span", "code-title", example.title));
    const button = make("button", "copy", "Copy");
    button.type = "button";
    button.setAttribute("aria-label", "Copy " + example.title);
    bar.append(button);
    const pre = make("pre");
    const code = make("code", "", example.code);
    code.dataset.codeLanguage = language;
    pre.append(code);
    panel.append(bar, pre);
    return panel;
  }
  components.forEach((component, index) => {
    const number = String(index + 1).padStart(2, "0");
    const link = make("a");
    link.href = "#" + component.id;
    link.append(make("span", "", number), document.createTextNode(component.title));
    $("#navigation").append(link);
    const article = make("article", "component");
    article.id = component.id;
    const head = make("div", "component-header");
    const title = make("div");
    const heading = make("h2", "", component.title);
    heading.id = component.id + "-heading";
    article.setAttribute("aria-labelledby", heading.id);
    title.append(heading, make("p", "", component.summary));
    head.append(make("div", "number", number), title);
    const content = make("div", "component-content");
    if (component.use) {
      const use = make("p", "use-case");
      use.append(make("b", "", "Use it for "), document.createTextNode(component.use));
      content.append(use);
    }
    if (component.setup) {
      const details = make("details");
      details.append(make("summary", "", "Create the contract and generate the bindings"));
      for (const example of component.setup) details.append(codePanel(example, "shared"));
      content.append(details);
    }
    for (const example of component.examples ?? []) content.append(codePanel(example, "shared"));
    for (const language of ["ts", "go"]) {
      for (const example of component[language] ?? []) content.append(codePanel(example, language));
    }
    const note = make("p", "note");
    note.append(make("strong", "", "Behavior to know. "), document.createTextNode(component.notes));
    content.append(note);
    article.append(head, content);
    article.dataset.search = (component.title + " " + article.textContent).toLowerCase();
    $("#components").append(article);
  });

  function highlight(code) {
    const source = code.textContent;
    const pattern = /(\/\/[^\n]*|#[^\n]*)|("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*')|\b(import|from|const|let|await|async|return|if|else|throw|new|for|of|as|type|interface|undefined|null|true|false|package|func|defer|var|nil|struct|map|any|error|range|string|bool|syntax|service|rpc|returns|message)\b|\b(\d[\d_]*)\b/g;
    const fragment = document.createDocumentFragment();
    let last = 0;
    for (const match of source.matchAll(pattern)) {
      fragment.append(document.createTextNode(source.slice(last, match.index)));
      const token = match[1] ? "comment" : match[2] ? "string" : match[3] ? "keyword" : "number";
      fragment.append(make("span", "token-" + token, match[0]));
      last = match.index + match[0].length;
    }
    fragment.append(document.createTextNode(source.slice(last)));
    code.replaceChildren(fragment);
  }
  document.querySelectorAll("pre code").forEach(highlight);

  function setLanguage(language, persist) {
    if (!["ts", "go"].includes(language)) return;
    document.body.dataset.language = language;
    document.querySelectorAll("[data-language-choice]").forEach(button => {
      button.setAttribute("aria-pressed", String(button.dataset.languageChoice === language));
    });
    if (persist) {
      try { localStorage.setItem("keel-sdk-language", language); } catch (_) {}
      if (window.openai?.setWidgetState) window.openai.setWidgetState({ language });
    }
  }
  try { setLanguage(localStorage.getItem("keel-sdk-language"), false); } catch (_) {}
  if (window.openai?.widgetState?.language) setLanguage(window.openai.widgetState.language, false);
  window.addEventListener("openai:set_globals", event => {
    const language = event.detail?.globals?.widgetState?.language;
    if (language) setLanguage(language, false);
  });
  document.querySelectorAll("[data-language-choice]").forEach(button => {
    button.addEventListener("click", () => setLanguage(button.dataset.languageChoice, true));
  });

  const label = document.body.dataset.reference ?? "components";
  function filter() {
    const query = $("#search").value.trim().toLowerCase();
    let count = 0;
    document.querySelectorAll(".component").forEach(article => {
      article.hidden = !article.dataset.search.includes(query);
      const link = $("#navigation a[href='#" + article.id + "']");
      link.hidden = article.hidden;
      if (!article.hidden) count++;
    });
    $("#result-count").textContent = query ? count + " of " + components.length + " " + label : "Showing all " + components.length + " " + label;
    $("#empty").hidden = count !== 0;
  }
  filter();
  $("#search").addEventListener("input", filter);
  $("#clear-search").addEventListener("click", () => { $("#search").value = ""; filter(); $("#search").focus(); });
  document.addEventListener("keydown", event => {
    if (event.key === "/" && !event.ctrlKey && !event.metaKey && !["INPUT", "TEXTAREA", "SELECT"].includes(document.activeElement.tagName)) {
      event.preventDefault(); $("#search").focus();
    }
    if (event.key === "Escape" && document.activeElement === $("#search")) { $("#search").value = ""; filter(); }
  });
  document.addEventListener("click", async event => {
    const button = event.target.closest(".copy");
    if (!button) return;
    const code = button.closest(".code-panel").querySelector("code");
    try {
      await navigator.clipboard.writeText(code.textContent);
      button.textContent = "Copied";
      $("#copy-status").textContent = "Code copied to clipboard";
    } catch (_) {
      const range = document.createRange();
      range.selectNodeContents(code);
      const selection = window.getSelection();
      selection.removeAllRanges(); selection.addRange(range);
      button.textContent = "Selected";
      $("#copy-status").textContent = "Code selected. Use your keyboard's copy command.";
    }
    setTimeout(() => button.textContent = "Copy", 1800);
  });
  const observer = new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      document.querySelectorAll("#navigation a").forEach(link => {
        if (link.hash === "#" + entry.target.id) link.setAttribute("aria-current", "location");
        else link.removeAttribute("aria-current");
      });
    }
  }, { rootMargin: "-90px 0px -60% 0px" });
  document.querySelectorAll(".component").forEach(article => observer.observe(article));

}
