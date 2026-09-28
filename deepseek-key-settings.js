(() => {
    const container = document.getElementById("deepseek-key-settings");
    if (!container) return;

    const form = document.createElement("form");
    form.className = "deepseek-key-form";
    const label = document.createElement("label");
    label.textContent = "DeepSeek API Key";
    const input = document.createElement("input");
    input.type = "password";
    input.name = "deepseek-api-key";
    input.placeholder = "粘贴 Key 后保存";
    input.autocomplete = "off";
    input.required = true;
    label.append(input);
    const save = document.createElement("button");
    save.type = "submit";
    save.textContent = "保存到本次浏览器会话";
    const remove = document.createElement("button");
    remove.type = "button";
    remove.textContent = "移除";
    const status = document.createElement("span");
    status.className = "deepseek-key-status";
    status.setAttribute("role", "status");
    form.append(label, save, remove, status);
    container.replaceChildren(form);
    const canConfigure = location.protocol === "https:" ||
        location.hostname === "localhost" || /^127\./.test(location.hostname) ||
        ["[::1]", "::1"].includes(location.hostname);
    input.disabled = !canConfigure;
    save.disabled = !canConfigure;

    async function refresh() {
        const response = await fetch("/api/settings/deepseek-key", { cache: "no-store" });
        if (!response.ok) throw new Error("无法读取 Key 状态");
        const state = await response.json();
        status.textContent = state.source === "browser"
            ? "已配置：••••••••（当前浏览器）"
            : state.source === "server"
                ? "已配置：••••••••（服务器默认）"
                : "未配置 DeepSeek Key";
        if (!canConfigure) status.textContent += "；请通过 HTTPS 或本机地址配置";
        remove.disabled = !canConfigure || state.source !== "browser";
    }

    form.addEventListener("submit", async (event) => {
        event.preventDefault();
        save.disabled = true;
        try {
            const response = await fetch("/api/settings/deepseek-key", {
                method: "PUT",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ apiKey: input.value }),
            });
            if (!response.ok) throw new Error("保存失败，请检查 Key 是否为空");
            input.value = "";
            await refresh();
        } catch (error) {
            status.textContent = error.message;
        } finally {
            save.disabled = !canConfigure;
        }
    });
    remove.addEventListener("click", async () => {
        remove.disabled = true;
        try {
            const response = await fetch("/api/settings/deepseek-key", { method: "DELETE" });
            if (!response.ok) throw new Error("移除失败");
            await refresh();
        } catch (error) {
            status.textContent = error.message;
            remove.disabled = false;
        }
    });
    refresh().catch(() => { status.textContent = "无法读取 Key 状态"; });
})();
