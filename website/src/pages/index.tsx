import type {ReactNode} from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import Heading from '@theme/Heading';
import FeatureHighlights from '@site/src/components/FeatureHighlights';
import Reveal from '@site/src/components/Reveal';

import styles from './index.module.css';

function HomepageHeader() {
  return (
    <header className={clsx('hero hero--primary', styles.heroBanner)}>
      <div className="container">
        <div className={clsx(styles.heroGridSingle, 'text--center')}>
          <Heading as="h1" className="hero__title">
            Know the blast radius
            <br />
            before you deploy.
          </Heading>
          <p className="hero__subtitle">
            Kairo is a Kubernetes change-intelligence engine — it compares a
            cluster snapshot with proposed manifests, runs a deterministic
            capacity simulation, and produces a machine-readable
            SAFE/REVIEW/BLOCK verdict. One dependency-light Go binary: CLI,
            REST API, and web dashboard.
          </p>
          <div className={styles.buttons}>
            <Link
              className="button button--secondary button--lg"
              to="https://github.com/zyvorai/kairo#run-it">
              Get Started
            </Link>
            <Link
              className="button button--outline button--lg button--secondary"
              to="https://github.com/zyvorai/kairo">
              View on GitHub
            </Link>
          </div>
        </div>
      </div>
    </header>
  );
}

function ProblemStatement() {
  return (
    <section className={styles.problem}>
      <div className="container">
        <Reveal className="row">
          <div className="col col--8 col--offset-2 text--center">
            <Heading as="h2" className={styles.sectionHeading}>
              A pre-production decision aid
            </Heading>
            <p>
              Kairo models node allocatable CPU/memory/GPU capacity, existing
              bound Pod requests, and runs a greedy pod-fit simulation
              against your proposed change — flagging PVC shrinks,
              NetworkPolicy/CiliumNetworkPolicy changes, strict
              PodDisruptionBudget conflicts, and ResourceQuota pressure
              before you apply anything.
            </p>
            <p>
              It's explicit about its own limits: Kairo is a decision aid,
              not a replacement for Kubernetes admission, the real
              scheduler, policy engines, or progressive delivery. The
              dependency-free YAML reader supports the common manifest
              subset and JSON; advanced YAML anchors/tags aren't supported
              in this release.
            </p>
          </div>
        </Reveal>
      </div>
    </section>
  );
}

function TrustBand() {
  return (
    <section className={styles.trust}>
      <div className="container">
        <Reveal className={styles.trustGrid}>
          <div>
            <Heading as="h3" className={styles.sectionHeading}>
              Small on purpose
            </Heading>
            <p>
              Apache-2.0, one Go binary, no external dependencies for the
              simulation engine itself. Docker image, health endpoint,
              GitHub Actions CI, and unit/integration tests are all part of
              this release.
            </p>
            <Link to="/docs/ARCHITECTURE">Read the architecture direction →</Link>
          </div>
          <div className={styles.trustBadges}>
            <img
              src="https://img.shields.io/badge/license-Apache--2.0-blue.svg"
              alt="Apache 2.0 license"
            />
          </div>
        </Reveal>
      </div>
    </section>
  );
}

function EnterpriseCTA() {
  return (
    <section className={styles.enterprise}>
      <div className="container text--center">
        <Reveal>
          <Heading as="h2" className={styles.sectionHeading}>
            Need production support or SLAs?
          </Heading>
          <p className={styles.enterpriseCopy}>
            Kairo's core is Apache-2.0 and free to run in personal, lab, and
            commercial production use at no charge. Zyvor Enterprise adds
            production support, SLAs, and additional products for teams
            that need them.
          </p>
          <Link
            className="button button--primary button--lg"
            to="mailto:sales@zyvor.dev">
            Contact sales@zyvor.dev
          </Link>
        </Reveal>
      </div>
    </section>
  );
}

export default function Home(): ReactNode {
  return (
    <Layout
      title="Kairo — Kubernetes change-intelligence engine"
      description="Know the blast radius before you deploy. A Kubernetes change-intelligence engine: capacity simulation, blast-radius scoring, and a SAFE/REVIEW/BLOCK verdict.">
      <HomepageHeader />
      <main>
        <ProblemStatement />
        <Reveal>
          <FeatureHighlights />
        </Reveal>
        <TrustBand />
        <EnterpriseCTA />
      </main>
    </Layout>
  );
}
