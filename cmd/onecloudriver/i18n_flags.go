package main

import (
	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/spf13/cobra"
)

// setFlagUsage assigns the localized usage to a flag, looking it up among the
// command's local, persistent and inherited flags. It is a no-op when the
// command or flag does not exist.
func setFlagUsage(cmd *cobra.Command, name, usage string) {
	if cmd == nil {
		return
	}
	if f := cmd.Flags().Lookup(name); f != nil {
		f.Usage = usage
		return
	}
	if f := cmd.PersistentFlags().Lookup(name); f != nil {
		f.Usage = usage
		return
	}
	if f := cmd.InheritedFlags().Lookup(name); f != nil {
		f.Usage = usage
	}
}

// localizeFlags assigns the localized usage strings to every flag in the tree.
// Flag usages are registered in English at init time and must be re-assigned
// after i18n.Init (same as Short/Long). The ids are literal i18n.L("...")
// calls so TestCatalogCompleteness can verify them.
func localizeFlags(root *cobra.Command) {
	// Root persistent flags.
	setFlagUsage(root, "lang", i18n.L("flag.lang"))
	setFlagUsage(root, "log-level", i18n.L("flag.log.level"))
	setFlagUsage(root, "log-json", i18n.L("flag.log.json"))

	for _, c := range root.Commands() {
		// Shared helper flags, identical across commands. Per-command
		// overrides below win because they run afterwards.
		setFlagUsage(c, "account", i18n.L("flag.account"))
		setFlagUsage(c, "output", i18n.L("flag.output"))
		setFlagUsage(c, "etag", i18n.L("flag.etag"))
		setFlagUsage(c, "dest-id", i18n.L("flag.dest.id"))
		setFlagUsage(c, "dest-path", i18n.L("flag.dest.path"))

		switch c.Name() {
		case "list":
			setFlagUsage(c, "id", i18n.L("flag.list.id"))
			setFlagUsage(c, "path", i18n.L("flag.list.path"))
		case "info":
			setFlagUsage(c, "id", i18n.L("flag.info.id"))
			setFlagUsage(c, "path", i18n.L("flag.info.path"))
		case "download":
			setFlagUsage(c, "id", i18n.L("flag.download.id"))
			setFlagUsage(c, "path", i18n.L("flag.download.path"))
			setFlagUsage(c, "output", i18n.L("flag.download.output"))
			setFlagUsage(c, "output-dir", i18n.L("flag.download.output.dir"))
		case "mkdir":
			setFlagUsage(c, "id", i18n.L("flag.mkdir.id"))
			setFlagUsage(c, "path", i18n.L("flag.mkdir.path"))
			setFlagUsage(c, "name", i18n.L("flag.mkdir.name"))
		case "upload":
			setFlagUsage(c, "id", i18n.L("flag.upload.id"))
			setFlagUsage(c, "path", i18n.L("flag.upload.path"))
			setFlagUsage(c, "file", i18n.L("flag.upload.file"))
		case "rm":
			setFlagUsage(c, "id", i18n.L("flag.rm.id"))
			setFlagUsage(c, "path", i18n.L("flag.rm.path"))
			setFlagUsage(c, "force", i18n.L("flag.rm.force"))
		case "rename":
			setFlagUsage(c, "id", i18n.L("flag.rename.id"))
			setFlagUsage(c, "path", i18n.L("flag.rename.path"))
			setFlagUsage(c, "name", i18n.L("flag.rename.name"))
		case "mv":
			setFlagUsage(c, "id", i18n.L("flag.mv.id"))
			setFlagUsage(c, "path", i18n.L("flag.mv.path"))
		case "copy":
			setFlagUsage(c, "id", i18n.L("flag.copy.id"))
			setFlagUsage(c, "path", i18n.L("flag.copy.path"))
			setFlagUsage(c, "name", i18n.L("flag.copy.name"))
		case "account":
			for _, sub := range c.Commands() {
				if sub.Name() == "remove" {
					setFlagUsage(sub, "purge", i18n.L("flag.account.purge"))
					setFlagUsage(sub, "keep", i18n.L("flag.account.keep"))
				}
			}
		case "mount":
			setFlagUsage(c, "account", i18n.L("flag.mount.account"))
			setFlagUsage(c, "cache-dir", i18n.L("flag.mount.cache.dir"))
			setFlagUsage(c, "cache-max-entries", i18n.L("flag.mount.cache.max.entries"))
			setFlagUsage(c, "cache-max-size", i18n.L("flag.mount.cache.max.size"))
			setFlagUsage(c, "max-uploads", i18n.L("flag.mount.max.uploads"))
			setFlagUsage(c, "upload-retries", i18n.L("flag.mount.upload.retries"))
			setFlagUsage(c, "graph-retries", i18n.L("flag.mount.graph.retries"))
			setFlagUsage(c, "pre-warm-depth", i18n.L("flag.mount.pre.warm.depth"))
			setFlagUsage(c, "debug", i18n.L("flag.mount.debug"))
			setFlagUsage(c, "debug-addr", i18n.L("flag.mount.debug.addr"))
		case "service":
			for _, sub := range c.Commands() {
				switch sub.Name() {
				case "install":
					setFlagUsage(sub, "mountpoint", i18n.L("flag.service.mountpoint"))
					setFlagUsage(sub, "account", i18n.L("flag.service.account"))
					setFlagUsage(sub, "enable", i18n.L("flag.service.enable"))
					setFlagUsage(sub, "all", i18n.L("flag.service.install.all"))
				case "stop":
					setFlagUsage(sub, "all", i18n.L("flag.service.stop.all"))
				case "uninstall":
					setFlagUsage(sub, "all", i18n.L("flag.service.uninstall.all"))
				}
			}
		}
	}

	// cobra auto-registers --help on every command ("help for <name>").
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		setFlagUsage(c, "help", i18n.Ld("help.help_flag", map[string]any{"Command": c.Name()}))
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}
