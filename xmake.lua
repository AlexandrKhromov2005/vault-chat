-- Local checks of the project, as listed in CONTRIBUTING.md.
--
-- xmake runs Go, golangci-lint, govulncheck, Docker and Node.js; it builds
-- nothing itself. `xmake ci` runs every check and stops at the first
-- failure with a non-zero exit code.

set_project("vault-chat")

-- Minimum statement coverage of every business logic package.
local COVERAGE_MIN = 70

-- Test databases and storage that `xmake test-integration` creates in the
-- Compose stack. The tests clean their tables, so they never use the
-- service databases.
local TEST_DATABASES = {
    VAULT_CHAT_TEST_DATABASE_URL = "vault_chat_auth_test",
    VAULT_CHAT_TEST_CHAT_DATABASE_URL = "vault_chat_chat_test",
    VAULT_CHAT_TEST_MEDIA_DATABASE_URL = "vault_chat_media_test",
}
local TEST_S3 = "VAULT_CHAT_TEST_MEDIA_S3_ENDPOINT"

-- `xmake build` compiles every package.
target("vault-chat")
    set_kind("phony")
    on_build(function ()
        os.execv("go", {"build", "./..."})
    end)

-- check declares a task that runs commands, each a list of program and
-- arguments, one after another.
local function check(name, description, commands)
    task(name)
        set_menu({usage = "xmake " .. name, description = description, options = {}})
        on_run(function ()
            for _, command in ipairs(commands) do
                os.execv(command[1], table.slice(command, 2))
            end
        end)
end

check("fmt", "Format the code with gofmt and goimports.", {{"golangci-lint", "fmt"}})
check("fmt-check", "Check formatting without changing files.", {{"golangci-lint", "fmt", "--diff"}})
check("vet", "Run go vet.", {{"go", "vet", "./..."}})
check("lint", "Run golangci-lint.", {{"golangci-lint", "run"}})
check("deployment", "Check VPS scripts, backup safety and Compose policy.", {{"sh", "scripts/check-deployment.sh"}})
check("monitoring", "Validate monitoring exposure and Prometheus alert rules.", {{"sh", "scripts/check-monitoring.sh"}})
check("vuln", "Check reachable vulnerabilities with govulncheck.", {{"govulncheck", "./..."}})

task("setup")
    set_menu({usage = "xmake setup", description = "Download modules and install the pre-commit hook.", options = {}})
    on_run(function ()
        os.execv("go", {"mod", "download"})
        os.execv("git", {"config", "core.hooksPath", ".githooks"})
        print("pre-commit hook installed: fmt-check, vet and lint run before every commit")
    end)

-- Unit tests. Web client tests run when web/test has any; they need
-- Node.js 22+.
task("test")
    set_menu({usage = "xmake test", description = "Run unit tests.", options = {}})
    on_run(function ()
        import("scripts.xmake.tests").run({})
    end)

task("test-race")
    set_menu({usage = "xmake test-race", description = "Run unit tests with the race detector.", options = {}})
    on_run(function ()
        import("scripts.xmake.tests").run({race = true})
    end)

task("test-integration")
    set_menu({
        usage = "xmake test-integration",
        description = "Run tests with the race detector against PostgreSQL and S3 from Compose.",
        options = {}})
    on_run(function ()
        import("scripts.xmake.tests").run({race = true, integration = {databases = TEST_DATABASES, s3 = TEST_S3}})
    end)

task("test-coverage")
    set_menu({usage = "xmake test-coverage", description = "Check coverage of service and handler packages.", options = {}})
    on_run(function ()
        import("scripts.xmake.tests").coverage(COVERAGE_MIN)
    end)

task("fuzz")
    set_menu({
        usage = "xmake fuzz [options]",
        description = "Fuzz every target for a while.",
        options = {{"t", "time", "kv", "30s", "Fuzzing time per target."}}})
    on_run(function ()
        import("core.base.option")
        import("scripts.xmake.tests").fuzz(option.get("time"))
    end)

task("fuzz-extended")
    set_menu({usage = "xmake fuzz-extended", description = "Fuzz every target for 10 minutes.", options = {}})
    on_run(function ()
        import("scripts.xmake.tests").fuzz("10m")
    end)

task("ci")
    set_menu({usage = "xmake ci", description = "Run every check; stop at the first failure.", options = {}})
    on_run(function ()
        import("core.project.task")
        local steps = {
            {"fmt-check"}, {"vet"}, {"lint"}, {"build"}, {"test-integration"},
            {"test-coverage"}, {"deployment"}, {"monitoring"}, {"fuzz", {time = "10s"}}, {"vuln"},
        }
        local timings = {}
        for _, step in ipairs(steps) do
            local name = step[1]
            cprint("${bright}==> xmake %s${clear}", name)
            local started = os.mclock()
            try {
                function ()
                    task.run(name, step[2] or {})
                end,
                catch {
                    function (errors)
                        cprint("${bright red}xmake ci failed at %s${clear}", name)
                        raise(errors)
                    end
                }
            }
            table.insert(timings, string.format("%-17s %6.1fs", name, (os.mclock() - started) / 1000))
        end
        cprint("${bright green}xmake ci passed${clear}")
        for _, line in ipairs(timings) do
            print("  " .. line)
        end
    end)
