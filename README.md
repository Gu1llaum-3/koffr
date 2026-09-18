# Kit de pilotage d'un projet par Claude Code

Ce dossier est un dépôt cible en miniature : on le copie à la racine d'un projet vide, puis on
lance `/demarrer-projet` sur le cahier des charges. Il contient la méthode (`METHODE.md`), les
documents de pilotage vides, leurs gabarits, et les skills qui les font vivre.

Il est né de Pilot v2 (septembre 2026) : ce qui y a marché est gardé, ce qui a dérivé est corrigé
(voir « Pourquoi ces choix » en bas).

## Installer

```sh
cp -R kit-projet/. <nouveau-depot>/
cd <nouveau-depot>
rm README.md                                   # ce fichier ne concerne que le kit
cp CLAUDE.local.md.example CLAUDE.local.md      # préférences locales, gitignoré
mkdir -p docs/cdc && cp <le cahier des charges> docs/cdc/
git init && git add -A && git commit -m "chore: bootstrap project steering kit"
```

Puis, dans Claude Code : `/demarrer-projet docs/cdc/<fichier>`. Le skill s'arrête trois fois pour
validation (exigences, ADR proposés, roadmap) ; on ne le laisse pas enchaîner.

## Ce qu'il y a dedans

| Fichier                              | Rôle                                                                                         |
| ------------------------------------ | -------------------------------------------------------------------------------------------- |
| `METHODE.md`                         | La méthode : cycle, rôle de chaque document, registres, règles d'exécution, Definition of done |
| `CLAUDE.md`                          | Ce que Claude lit à chaque session : où lire, quoi lancer, conventions non déductibles du code |
| `ARCHITECTURE.md`                    | Structure du code et règles de dépendance, à remplir après l'ADR de stack                    |
| `ROADMAP.md`                         | Vue macro : un lot par ligne, son critère de sortie, son plan. Pas de sous-tâches            |
| `CLAUDE.local.md.example`            | Gabarit des préférences locales (chemins, ports, comptes de dev), jamais commité             |
| `docs/README.md`                     | Index de `docs/` et **table des registres** (un préfixe par registre, sans collision)        |
| `docs/cdc/`                          | Le cahier des charges reçu, son analyse et le registre d'exigences `E-nn`                    |
| `docs/adr/`                          | Décisions figées `ADR-NNNN`, gabarit `0000-template.md`                                      |
| `docs/decisions.md`                  | Arbitrages en attente `D-nn` (budget, calendrier, technique)                                 |
| `docs/questions.md`                  | Questions au métier `Q-nn`, avec leurs réponses datées                                       |
| `docs/backlog.md`                    | Ce qu'on a refusé de faire maintenant `B-nn`, avec ce que ça coûterait                        |
| `docs/plans/`                        | Un plan par lot, gabarit `0000-template.md`, exécuté par `/executer-plan`                    |
| `docs/retro/`                        | Une rétrospective par lot, gabarit `0000-template.md`, écrite par `/cloturer-lot`            |
| `docs/recette/`                      | Scénarios de recette et anomalies `A-nn`                                                     |
| `docs/modeles/rules.md`              | Gabarit du `rules.md` d'un module (règles métier tracées, une par test)                      |
| `.claude/skills/demarrer-projet/`    | Du cahier des charges aux documents de pilotage, en trois arrêts                             |
| `.claude/skills/ecrire-plan/`        | De la roadmap au plan d'un lot, conforme au gabarit                                          |
| `.claude/skills/executer-plan/`      | Exécution d'un plan validé, vague par vague                                                  |
| `.claude/skills/ecrire-adr/`         | D'une décision prise à son ADR                                                               |
| `.claude/skills/cloturer-lot/`       | Rétro, roadmap cochée, leçons fondues dans le skill de stack, mémoire purgée                  |
| `.claude/skills/implementer/`        | Le skill de stack : gabarit, rempli au lot 0 pour la stack choisie                           |

## Ce qu'on adapte à chaque projet

- `CLAUDE.md` § Commandes, § Conventions de la stack, § Règles d'architecture : après l'ADR de stack.
- `ARCHITECTURE.md` en entier.
- `.claude/skills/implementer/SKILL.md` : les exemples canoniques de la stack.
- La langue des textes d'interface (ADR de langues) : le kit fixe le français pour le pilotage et
  l'anglais pour le code, pas la langue des utilisateurs.

## Profil « réécriture d'un existant »

Rare, non inclus par défaut. Il ajoute : un lien `legacy/` vers le code source de l'existant
(gitignoré, chemin dans `CLAUDE.local.md`), des relevés d'écrans dans `docs/ui-reference/`, un
registre de défauts de l'existant `L-nn`, une reprise de données rejouable par tranche verticale,
un audit 1:1 pendant lequel on ne corrige rien, et la règle « mesurer sur les données réelles
avant de modéliser ». Pilot v2 en est la référence (`docs/ui-reference/README.md`,
`docs/defauts-legacy.md`, `scripts/replicate-legacy/`, ADR-0009, 0011, 0020).

## Pourquoi ces choix

- **Une décision qui n'est pas écrite ne se code pas.** C'est ce qui empêche le modèle de trancher à
  la place du propriétaire. D'où les registres et les ADR en statut « proposé » tant qu'un humain
  n'a pas dit oui.
- **Un état, pas un journal.** Sur Pilot v2, `ROADMAP.md` a fini à 450 lignes et 96 cases, le skill
  de stack à 363 lignes organisées par « leçons du lot N ». Ici : la roadmap ne porte que des
  critères de sortie, les cases vivent dans les plans, le récit vit dans `docs/retro/`, et le skill
  reste thématique et au présent.
- **Un préfixe par registre.** Pilot v2 a eu deux registres en `D-nn` (décisions et défauts) et des
  renvois du type « question Q3 — Q2 du registre ». La table de `docs/README.md` est la seule
  source des préfixes.
- **Les règles vivent dans l'outillage.** Une règle que le lint ou la CI vérifie n'a pas besoin
  d'être répétée au modèle. `CLAUDE.md` ne garde que ce qu'on ne peut pas déduire ni vérifier.
- **Tout ce que la méthode requiert est versionné.** Les skills vivent dans `.claude/skills/` du
  dépôt, pas dans `~/.claude/`. Le dépôt distant et la CI verte sont dans le lot 0, pas « à
  ajouter plus tard » (Pilot v2 a fait six lots sans remote).
- **La mémoire de Claude ne porte pas l'état du projet.** Elle garde les préférences et les
  retours ; l'état vit dans le dépôt, sinon les deux dérivent.
