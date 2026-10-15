import { Link } from 'react-router';
import { Muted } from '../components/QueryState';

export function NotFoundPage() {
  return (
    <Muted>
      Nothing here.{' '}
      <Link to="/queues" className="text-foreground underline underline-offset-4">
        Go to queues
      </Link>
    </Muted>
  );
}
