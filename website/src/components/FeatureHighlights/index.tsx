import type {ReactNode} from 'react';
import Link from '@docusaurus/Link';
import Heading from '@theme/Heading';
import styles from './styles.module.css';

type FeatureItem = {
  title: string;
  description: ReactNode;
  to: string;
};

const FeatureList: FeatureItem[] = [
  {
    title: 'Deterministic capacity simulation',
    description:
      'Node allocatable CPU, memory, and nvidia.com/gpu capacity modelling, plus existing bound Pod request accounting, against your proposed manifests.',
    to: 'https://github.com/zyvorai/kairo#what-works-in-this-release',
  },
  {
    title: 'Workload-aware modelling',
    description:
      'Deployment, StatefulSet, DaemonSet, Pod, Job, and KubeVirt VirtualMachine request modelling, with greedy pod-fit simulation and unschedulable-replica reporting.',
    to: 'https://github.com/zyvorai/kairo#what-works-in-this-release',
  },
  {
    title: 'Blast-radius verdict',
    description:
      'A blast-radius score, LOW/MEDIUM/HIGH level, and a SAFE/REVIEW/BLOCK verdict — machine-readable via the CLI\'s -json flag or the REST API.',
    to: 'https://github.com/zyvorai/kairo#cli',
  },
  {
    title: 'Policy and quota change warnings',
    description:
      'PVC shrink detection, NetworkPolicy/CiliumNetworkPolicy/CiliumClusterwideNetworkPolicy change warnings, strict PodDisruptionBudget detection, and ResourceQuota pressure checks.',
    to: 'https://github.com/zyvorai/kairo#what-works-in-this-release',
  },
  {
    title: 'CLI, REST API, and web UI',
    description:
      'One Go binary serves all three — a polished zero-build web dashboard, a JSON REST API, and a CLI with clear exit codes (0 SAFE/REVIEW, 3 BLOCK).',
    to: 'https://github.com/zyvorai/kairo#rest-api',
  },
  {
    title: 'Docker, CI, and remote deploy',
    description:
      'A Docker image, health endpoint, GitHub Actions CI with unit/integration tests, and a remote systemd deploy + smoke script.',
    to: 'https://github.com/zyvorai/kairo#docker',
  },
];

function Feature({title, description, to}: FeatureItem) {
  return (
    <div className="col col--4">
      <Link to={to} className={styles.card}>
        <Heading as="h3">{title}</Heading>
        <p>{description}</p>
      </Link>
    </div>
  );
}

export default function FeatureHighlights(): ReactNode {
  return (
    <section className={styles.features}>
      <div className="container">
        <div className="row">
          {FeatureList.map((props, idx) => (
            <Feature key={idx} {...props} />
          ))}
        </div>
      </div>
    </section>
  );
}
