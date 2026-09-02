package main

import (
	"fmt"

	"github.com/frosado/onecloudriver/internal/i18n"
	"github.com/spf13/cobra"
)

// resolveLanguage returns the language for this run: the --lang flag if set,
// otherwise the detected POSIX locale, normalized to a base BCP-47 tag
// (issue #30, plan §8.3: --lang accepts es_ES, es_ES.UTF-8, etc.).
func resolveLanguage(cmd *cobra.Command) string {
	lang, _ := cmd.Flags().GetString("lang")
	if lang == "" {
		lang = i18n.DetectLanguage()
	}
	return i18n.ParseLocale(lang)
}

// localizeCommandTree assigns the localized Short/Long to every command in
// the tree. These texts are set in English at init time and must be re-assigned
// after i18n.Init (cobra does not run PersistentPreRun for --help, so this is
// also invoked from the localized help funcs). The ids are literal i18n.L("...")
// calls so TestCatalogCompleteness can verify them.
func localizeCommandTree(root *cobra.Command) {
	assignCommandTexts(root)
	for _, c := range root.Commands() {
		assignCommandTexts(c)
		for _, sub := range c.Commands() {
			assignCommandTexts(sub)
		}
	}
}

// assignCommandTexts sets Short/Long for one command based on its full path.
func assignCommandTexts(c *cobra.Command) {
	switch c.CommandPath() {
	case "onecloudriver":
		c.Short = i18n.L("cmd.root.short")
	case "onecloudriver account":
		c.Short = i18n.L("cmd.account.short")
	case "onecloudriver account add":
		c.Short = i18n.L("cmd.account.add.short")
	case "onecloudriver account list":
		c.Short = i18n.L("cmd.account.list.short")
	case "onecloudriver account remove":
		c.Short = i18n.L("cmd.account.remove.short")
		c.Long = i18n.L("cmd.account.remove.long")
	case "onecloudriver completion":
		c.Short = i18n.L("cmd.completion.short")
	case "onecloudriver copy":
		c.Short = i18n.L("cmd.copy.short")
		c.Long = i18n.L("cmd.copy.long")
	case "onecloudriver download":
		c.Short = i18n.L("cmd.download.short")
		c.Long = i18n.L("cmd.download.long")
	case "onecloudriver help":
		c.Short = i18n.L("cmd.help.short")
	case "onecloudriver info":
		c.Short = i18n.L("cmd.info.short")
		c.Long = i18n.L("cmd.info.long")
	case "onecloudriver list":
		c.Short = i18n.L("cmd.list.short")
		c.Long = i18n.L("cmd.list.long")
	case "onecloudriver mkdir":
		c.Short = i18n.L("cmd.mkdir.short")
	case "onecloudriver mount":
		c.Short = i18n.L("cmd.mount.short")
		c.Long = i18n.L("cmd.mount.long")
	case "onecloudriver mv":
		c.Short = i18n.L("cmd.mv.short")
	case "onecloudriver rename":
		c.Short = i18n.L("cmd.rename.short")
	case "onecloudriver rm":
		c.Short = i18n.L("cmd.rm.short")
	case "onecloudriver service":
		c.Short = i18n.L("cmd.service.short")
		c.Long = i18n.L("cmd.service.long")
	case "onecloudriver service install":
		c.Short = i18n.L("cmd.service.install.short")
		c.Long = i18n.L("cmd.service.install.long")
	case "onecloudriver service uninstall":
		c.Short = i18n.L("cmd.service.uninstall.short")
		c.Long = i18n.L("cmd.service.uninstall.long")
	case "onecloudriver service list":
		c.Short = i18n.L("cmd.service.list.short")
	case "onecloudriver service status":
		c.Short = i18n.L("cmd.service.status.short")
	case "onecloudriver service start":
		c.Short = i18n.L("cmd.service.start.short")
	case "onecloudriver service stop":
		c.Short = i18n.L("cmd.service.stop.short")
		c.Long = i18n.L("cmd.service.stop.long")
	case "onecloudriver sync":
		c.Short = i18n.L("cmd.sync.short")
		c.Long = i18n.L("cmd.sync.long")
	case "onecloudriver upload":
		c.Short = i18n.L("cmd.upload.short")
		c.Long = i18n.L("cmd.upload.long")
	}
}

// localizedUsageTemplate returns the cobra usage template with the section
// titles localized via L(). It is built after i18n.Init so L() resolves to the
// active language; the cobra placeholders ({{.UseLine}}, {{.Short}}, ...) are
// left intact.
func localizedUsageTemplate() string {
	return fmt.Sprintf(`%s:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

%s:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

%s:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

%s:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

%s:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

%s:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

%s:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

%s "{{.CommandPath}} [command] --help" %s{{end}}
`,
		i18n.L("help.usage"),
		i18n.L("help.aliases"),
		i18n.L("help.examples"),
		i18n.L("help.available_commands"),
		i18n.L("help.flags"),
		i18n.L("help.global_flags"),
		i18n.L("help.additional_topics"),
		i18n.L("help.use"),
		i18n.L("help.more_info"),
	)
}

// refreshLocale re-initializes the locale from the command's flags and
// re-applies the localized Short/Long and usage template. It is the shared
// entry point for PersistentPreRun and the help/usage funcs.
func refreshLocale(cmd *cobra.Command) {
	i18n.Init(resolveLanguage(cmd))
	localizeCommandTree(cmd.Root())
	localizeFlags(cmd.Root())
	cmd.Root().SetUsageTemplate(localizedUsageTemplate())
}

// setupLocalizedHelp installs help/usage funcs on the root command that first
// refresh the locale and then delegate to cobra's default renderer. cobra
// inherits these funcs down the command tree (subcommands without their own
// help func fall back to the parent's).
func setupLocalizedHelp(root *cobra.Command) {
	defaultHelp := root.HelpFunc()
	defaultUsage := root.UsageFunc()

	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		refreshLocale(cmd)
		defaultHelp(cmd, args)
	})
	root.SetUsageFunc(func(cmd *cobra.Command) error {
		refreshLocale(cmd)
		return defaultUsage(cmd)
	})
}
