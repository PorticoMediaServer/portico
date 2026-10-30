import {defineWebAction} from '../../route-registry';
import {FeedbackHost} from './FeedbackHost';
import {feedbackAvailable, openFeedback, subscribeFeedback} from './feedback';

export default defineWebAction({id: 'feedback', host: FeedbackHost, subscribe: subscribeFeedback, getSnapshot: feedbackAvailable, invoke: () => openFeedback()});
