export const SECTIONS = ['Vue d’ensemble', 'Hôtes', 'Conteneurs', 'Journaux', 'Stockage'] as const
export type Section = (typeof SECTIONS)[number]

/** Current section, plus the container to open in the log viewer. */
class Nav {
  section = $state<Section>('Vue d’ensemble')
  logTarget = $state<{ host: string; container: string } | null>(null)
  /** Open the Containers page with "problems only" checked. */
  containerIssues = $state(false)

  openContainerIssues() {
    this.containerIssues = true
    this.section = 'Conteneurs'
  }

  openLogs(host: string, container: string) {
    this.logTarget = { host, container }
    this.section = 'Journaux'
  }
}

export const nav = new Nav()
