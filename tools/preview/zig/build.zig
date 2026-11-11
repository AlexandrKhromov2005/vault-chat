const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});
    const stb = b.dependency("stb", .{});
    const patch = b.addSystemCommand(&.{"python3"});
    patch.addFileArg(b.path("src/patch_stb.py"));
    patch.addFileArg(stb.path("stb_image.h"));
    const patched = patch.addOutputFileArg("include/stb_image.h");
    const translated = b.addTranslateC(.{
        .root_source_file = b.path("src/stb_api.h"),
        .target = target,
        .optimize = optimize,
    });
    translated.addIncludePath(patched.dirname());
    translated.addIncludePath(stb.path("."));
    const module = b.createModule(.{
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
        .link_libc = true,
    });
    module.addImport("stb", translated.createModule());
    module.addIncludePath(patched.dirname());
    module.addIncludePath(stb.path("."));
    module.addCSourceFile(.{
        .file = b.path("src/stb.c"),
        .flags = &.{"-std=c99"},
    });

    const exe = b.addExecutable(.{
        .name = "preview-decoder",
        .root_module = module,
    });
    b.installArtifact(exe);

    const tests = b.addTest(.{ .root_module = exe.root_module });
    const run_tests = b.addRunArtifact(tests);
    const test_step = b.step("test", "Run decoder tests");
    test_step.dependOn(&run_tests.step);
}
