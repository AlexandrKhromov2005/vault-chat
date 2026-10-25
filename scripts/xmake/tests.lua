-- Test helpers for the tasks in xmake.lua.

import("lib.detect.find_tool")

-- Certified CI owns disposable infrastructure. Manual integration runs must
-- explicitly select all test endpoints; never reuse the development stack.
local function prepare_integration(integration)
    local missing = {}
    for variable, _ in pairs(integration.databases) do
        if not os.getenv(variable) then
            table.insert(missing, variable)
        end
    end
    if #missing == 0 and os.getenv(integration.s3) then
        print("using the test databases and S3 from the environment")
        return
    end

    raise("integration endpoints are incomplete; run xmake ci for disposable fixtures, or explicitly set all four VAULT_CHAT_TEST_* endpoints")
end

-- web_tests runs the web client's tests, if there are any.
local function web_tests()
    if #os.files("web/test/*.test.mjs") == 0 then
        return
    end
    if not find_tool("node") then
        raise("web/test needs Node.js 22+ (node is not in PATH)")
    end
    os.execv("node", {"--test", "web/test/*.test.mjs"})
end

-- run runs the Go tests, with the race detector and integration tests if
-- asked, and then the web client's tests.
function run(opts)
    if opts.integration then
        prepare_integration(opts.integration)
    end
    local args = {"test", "-count=1"}
    if opts.race then
        table.insert(args, "-race")
    end
    table.insert(args, "./...")
    os.execv("go", args)
    web_tests()
end

-- coverage checks that every service and handler package reaches the
-- minimum statement coverage.
function coverage(minimum)
    local packages = {}
    for _, pkg in ipairs(os.iorunv("go", {"list", "./internal/..."}):split("\n")) do
        if pkg:endswith("/service") or pkg:endswith("/handler") then
            table.insert(packages, pkg)
        end
    end
    local output = os.iorunv("go", table.join({"test", "-count=1", "-cover"}, packages))
    local low = {}
    for _, pkg in ipairs(packages) do
        local percent = output:match(pkg:gsub("%p", "%%%0") .. "%s.-coverage: ([%d%.]+)%% of statements")
        local value = tonumber(percent) or 0
        print(string.format("%6.1f%%  %s", value, pkg))
        if value < minimum then
            table.insert(low, pkg)
        end
    end
    if #low > 0 then
        raise("coverage below %d%%: %s", minimum, table.concat(low, ", "))
    end
end

-- fuzz_targets lists {package, target} for every Fuzz function in tests.
local function fuzz_targets()
    local targets = {}
    local listing = os.iorunv("go", {"list", "-f",
        "{{.ImportPath}}|{{.Dir}}|{{join .TestGoFiles \",\"}},{{join .XTestGoFiles \",\"}}", "./..."})
    for _, line in ipairs(listing:split("\n")) do
        local pkg, dir, files = table.unpack(line:split("|", {strict = true}))
        for _, file in ipairs((files or ""):split(",")) do
            local source = io.readfile(path.join(dir, file))
            for name in source:gmatch("\nfunc (Fuzz[%w_]+)%(") do
                table.insert(targets, {pkg, name})
            end
        end
    end
    return targets
end

-- fuzz runs every fuzz target for the given time, one after another.
function fuzz(duration)
    local targets = fuzz_targets()
    print("fuzzing %d targets for %s each", #targets, duration)
    for _, target in ipairs(targets) do
        os.execv("go", {"test", target[1], "-run", "^$", "-fuzz", "^" .. target[2] .. "$",
            "-fuzztime", duration, "-parallel", "2"})
    end
end
