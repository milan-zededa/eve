// static/dashboard.js

document.addEventListener("DOMContentLoaded", () => {
    const tabs = document.querySelectorAll(".tab");
    const tabContents = {
        overview: document.getElementById("tab-overview"),
        components: document.getElementById("tab-components"),
        apps: document.getElementById("tab-apps"),
        raw: document.getElementById("tab-raw"),
    };

    // --- Tabs handling ---
    tabs.forEach((tab) => {
        tab.addEventListener("click", () => {
            const target = tab.getAttribute("data-tab");
            tabs.forEach((t) => t.classList.remove("active"));
            tab.classList.add("active");
            Object.entries(tabContents).forEach(([key, el]) => {
                el.classList.toggle("hidden", key !== target);
            });
        });
    });

    const refreshBtn = document.getElementById("refresh-btn");
    const errorBanner = document.getElementById("error-banner");

    function statusClass(status) {
        if (!status) return "";
        status = status.toLowerCase();
        if (status === "ok" || status === "healthy") return "ok";
        if (status === "warning") return "warning";
        return "critical";
    }

    function setPillStatus(element, status) {
        element.classList.remove("ok", "warning", "critical");
        const cls = statusClass(status);
        if (cls) {
            element.classList.add(cls);
        }
    }

    async function fetchReport() {
        errorBanner.textContent = "";
        if (refreshBtn) refreshBtn.disabled = true;
        try {
            const res = await fetch("/api/health/live");
            if (!res.ok) {
                throw new Error("HTTP " + res.status);
            }
            const data = await res.json();
            renderReport(data);
        } catch (err) {
            console.error("Failed to fetch health report", err);
            errorBanner.textContent = "Failed to load health report: " + err.message;
        } finally {
            if (refreshBtn) refreshBtn.disabled = false;
        }
    }

    function renderReport(report) {
        // --- Top info ---
        const nodeIdEl = document.getElementById("node-id");
        if (nodeIdEl) {
            nodeIdEl.textContent = "node: " + (report.node_id || "—");
        }

        const overallStatusText = document.getElementById("overall-status-text");
        if (overallStatusText) {
            overallStatusText.textContent = report.overall_status || "—";
        }

        const overallPill = document.getElementById("overall-status-pill");
        if (overallPill) {
            setPillStatus(overallPill, report.overall_status);
        }

        const safePill = document.getElementById("safe-to-deploy-pill");
        if (safePill && report.safe_to_deploy_new_app) {
            safePill.textContent =
                "Safe to deploy: " + (report.safe_to_deploy_new_app.status || "—");
        }

        const genAtEl = document.getElementById("generated-at");
        if (genAtEl) {
            const genAt = report.generated_at ? new Date(report.generated_at) : null;
            genAtEl.textContent =
                "Generated at: " + (genAt ? genAt.toLocaleString() : "—");
        }

        // --- Score & summary (Overview tab) ---
        const scoreCircle = document.getElementById("score-circle");
        const score = report.summary ? report.summary.health_score : null;
        if (scoreCircle) {
            scoreCircle.textContent = score != null ? score : "--";
            scoreCircle.classList.remove("ok", "warning", "critical");
            if (typeof score === "number") {
                if (score >= 80) scoreCircle.classList.add("ok");
                else if (score >= 60) scoreCircle.classList.add("warning");
                else scoreCircle.classList.add("critical");
            }
        }

        const statusLabelEl = document.getElementById("status-label");
        if (statusLabelEl) {
            statusLabelEl.textContent =
                "Status: " + (report.summary ? report.summary.status_label : "—");
        }

        const mainIssuesList = document.getElementById("main-issues-list");
        if (mainIssuesList) {
            mainIssuesList.innerHTML = "";
            if (
                report.summary &&
                Array.isArray(report.summary.main_issues) &&
                report.summary.main_issues.length > 0
            ) {
                report.summary.main_issues.forEach((issue) => {
                    const li = document.createElement("li");

                    const title = document.createElement("span");
                    title.className = "title";
                    title.textContent = issue.title || "(no title)";

                    const detail = document.createElement("div");
                    detail.className = "muted";
                    detail.textContent = issue.detail || "";

                    const meta = document.createElement("div");
                    meta.className = "muted";
                    meta.textContent =
                        "Severity: " +
                        (issue.severity || "—") +
                        " · Component: " +
                        (issue.component_type || "—");

                    li.appendChild(title);
                    li.appendChild(detail);
                    li.appendChild(meta);
                    mainIssuesList.appendChild(li);
                });
            } else {
                const li = document.createElement("li");
                li.className = "muted";
                li.textContent = "No issues detected.";
                mainIssuesList.appendChild(li);
            }
        }

        // --- Components tab ---
        const comps = report.components || {};
        renderComponentCard("comp-cpu", comps.cpu);
        renderComponentCard("comp-memory", comps.memory);
        renderComponentCard("comp-storage", comps.storage);
        renderComponentCard("comp-network", comps.network);
        renderComponentCard("comp-thermal", comps.thermal);
        renderComponentCard("comp-psu", comps.psu);

        // Replacement recommendations
        const replComment = document.getElementById("replacement-comment");
        const replList = document.getElementById("replacement-list");
        if (replComment && replList) {
            replList.innerHTML = "";
            if (report.hardware_replacement_recommendations) {
                replComment.textContent =
                    report.hardware_replacement_recommendations.overall_comment || "—";
                const items =
                    report.hardware_replacement_recommendations.priority_order || [];
                if (items.length === 0) {
                    const li = document.createElement("li");
                    li.className = "muted";
                    li.textContent = "No components currently marked for replacement.";
                    replList.appendChild(li);
                } else {
                    items.forEach((it) => {
                        const li = document.createElement("li");

                        const title = document.createElement("span");
                        title.className = "title";
                        title.textContent =
                            (it.component_type || "component") + " " + (it.id || "—");

                        const detail = document.createElement("div");
                        detail.className = "muted";
                        detail.textContent =
                            "Priority: " +
                            (it.priority || "—") +
                            " · " +
                            (it.reason || "");

                        li.appendChild(title);
                        li.appendChild(detail);
                        replList.appendChild(li);
                    });
                }
            } else {
                replComment.textContent = "—";
            }
        }

        // --- Apps tab ---
        const appsMig = document.getElementById("apps-migrate");
        const appsSafe = document.getElementById("apps-safe");

        if (appsMig) {
            appsMig.innerHTML = "";
            if (
                Array.isArray(report.apps_to_migrate) &&
                report.apps_to_migrate.length > 0
            ) {
                report.apps_to_migrate.forEach((app) => {
                    const li = document.createElement("li");

                    const title = document.createElement("span");
                    title.className = "title";
                    title.textContent =
                        app.app_name || app.app_id || "App";

                    const detail = document.createElement("div");
                    detail.className = "muted";
                    detail.textContent = "Priority: " + (app.priority || "—");

                    const reasons = document.createElement("div");
                    reasons.className = "muted";
                    if (Array.isArray(app.reasons)) {
                        reasons.textContent = app.reasons.join(" · ");
                    }

                    li.appendChild(title);
                    li.appendChild(detail);
                    li.appendChild(reasons);
                    appsMig.appendChild(li);
                });
            } else {
                const li = document.createElement("li");
                li.className = "muted";
                li.textContent = "None identified.";
                appsMig.appendChild(li);
            }
        }

        if (appsSafe) {
            appsSafe.innerHTML = "";
            if (
                Array.isArray(report.apps_safe_to_stay) &&
                report.apps_safe_to_stay.length > 0
            ) {
                report.apps_safe_to_stay.forEach((app) => {
                    const li = document.createElement("li");

                    const title = document.createElement("span");
                    title.className = "title";
                    title.textContent =
                        app.app_name || app.app_id || "App";

                    const detail = document.createElement("div");
                    detail.className = "muted";
                    detail.textContent = app.reason || "";

                    li.appendChild(title);
                    li.appendChild(detail);
                    appsSafe.appendChild(li);
                });
            } else {
                const li = document.createElement("li");
                li.className = "muted";
                li.textContent = "No apps listed.";
                appsSafe.appendChild(li);
            }
        }

        // --- Raw JSON tab ---
        const rawJsonEl = document.getElementById("raw-json");
        if (rawJsonEl) {
            rawJsonEl.textContent = JSON.stringify(report, null, 2);
        }
    }

    function renderComponentCard(cardId, comp) {
        const card = document.getElementById(cardId);
        if (!card) return;

        const statusEl = card.querySelector("[data-status-text]");
        const issuesList = card.querySelector("[data-issues-list]");
        if (!statusEl || !issuesList) return;

        const status = comp && comp.status ? comp.status : "—";
        statusEl.textContent = "Status: " + status;
        issuesList.innerHTML = "";

        let issues = [];

        if (!comp) {
            // nothing
        } else if (Array.isArray(comp.issues)) {
            issues = comp.issues;
        } else if (Array.isArray(comp.devices)) {
            // Storage: aggregate device issues
            comp.devices.forEach((d) => {
                if (Array.isArray(d.issues)) {
                    issues = issues.concat(d.issues);
                }
            });
        }

        if (issues.length === 0) {
            const li = document.createElement("li");
            li.className = "muted";
            li.textContent = "No issues.";
            issuesList.appendChild(li);
            return;
        }

        issues.forEach((issue) => {
            const li = document.createElement("li");

            const title = document.createElement("span");
            title.className = "title";
            title.textContent = issue.type || "Issue";

            const detail = document.createElement("div");
            detail.className = "muted";
            detail.textContent = issue.detail || "";

            li.appendChild(title);
            li.appendChild(detail);
            issuesList.appendChild(li);
        });
    }

    if (refreshBtn) {
        refreshBtn.addEventListener("click", fetchReport);
    }

    // Initial load
    fetchReport();
});
